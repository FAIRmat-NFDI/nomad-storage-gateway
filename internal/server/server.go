package server

import (
	"context"
	"crypto/subtle"
	"errors"
	"fmt"
	"maps"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	config "github.com/FAIRmat-NFDI/nomad-storage-gateway/internal/config"
	seaweedfs "github.com/FAIRmat-NFDI/nomad-storage-gateway/internal/seaweedfs"
	"github.com/aws/aws-sdk-go-v2/aws"
	v4 "github.com/aws/aws-sdk-go-v2/aws/signer/v4"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"
)

type Server struct {
	cfg            config.Config
	filerClient    filerLookupClient
	presigners     map[string]*s3.PresignClient
	signer         *v4.Signer
	publicEndpoint *url.URL
	now            func() time.Time
}

// centralSeaweedFSProvider is reserved for the gateway's internal SeaweedFS store.
const centralSeaweedFSProvider = "central_seaweedfs"

func NewRouter(cfg config.Config, filerClient filerLookupClient) (http.Handler, error) {
	return newRouter(cfg, filerClient, nil)
}

func newRouter(cfg config.Config, filerClient filerLookupClient, now func() time.Time) (http.Handler, error) {
	if _, ok := cfg.Providers[centralSeaweedFSProvider]; ok {
		return nil, fmt.Errorf("provider name %q is reserved", centralSeaweedFSProvider)
	}

	var publicEndpoint *url.URL
	if cfg.SeaweedFS.PublicEndpoint != "" {
		parsed, err := url.Parse(cfg.SeaweedFS.PublicEndpoint)
		if err != nil || parsed.Scheme == "" || parsed.Host == "" {
			if err == nil {
				err = errors.New("missing scheme or host")
			}
			return nil, fmt.Errorf("invalid seaweedfs.public_endpoint %q: %w", cfg.SeaweedFS.PublicEndpoint, err)
		}
		publicEndpoint = parsed
	}

	providers := maps.Clone(cfg.Providers)
	if providers == nil {
		providers = make(map[string]config.ObjectStore)
	}
	ctx := context.Background()

	providers[centralSeaweedFSProvider] = config.ObjectStore{
		Type:      "s3",
		Endpoint:  cfg.SeaweedFS.PublicEndpoint,
		Bucket:    cfg.SeaweedFS.S3Bucket,
		AccessKey: cfg.SeaweedFS.S3AccessKey,
		SecretKey: cfg.SeaweedFS.S3SecretKey,
	}

	presigners := make(map[string]*s3.PresignClient)
	for name, provider := range providers {
		presigner, err := newPresigner(ctx, provider)
		if err != nil {
			return nil, fmt.Errorf("create presigner for %q: %w", name, err)
		}
		presigners[name] = presigner

	}
	signer := v4.NewSigner(func(o *v4.SignerOptions) {
		o.DisableURIPathEscaping = true // S3 / boto3
	})

	if now == nil {
		now = func() time.Time { return time.Now().UTC() }
	}

	s := &Server{
		cfg:            cfg,
		filerClient:    filerClient,
		presigners:     presigners,
		signer:         signer,
		publicEndpoint: publicEndpoint,
		now:            now,
	}
	r := chi.NewRouter()

	r.Use(middleware.RequestID)
	r.Use(middleware.ClientIPFromRemoteAddr)
	r.Use(middleware.Logger)
	r.Use(middleware.Recoverer)

	r.Get("/health", s.health)
	r.Group(func(r chi.Router) {
		r.Use(s.requirePresignedQuery)
		r.Get("/zip/{upload_id}", s.zip)
		r.Get("/zip/{upload_id}/*", s.zip)
	})
	return r, nil
}

func (s *Server) health(w http.ResponseWriter, r *http.Request) {
	w.Write([]byte("ok"))
}

// Match the presigned URL by creating a new presigned URL and making sure the signatures match.
// The presigned URLs are created with the same access key and secret so if it doesn't match, the URL is invalid.
func (s *Server) requirePresignedQuery(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if s.publicEndpoint == nil {
			http.Error(w, "public endpoint not configured", http.StatusInternalServerError)
			return
		}

		query := r.URL.Query()

		if query.Get("X-Amz-Algorithm") != "AWS4-HMAC-SHA256" {
			http.Error(w, "invalid or missing X-Amz-Algorithm", http.StatusBadRequest)
			return
		}

		signature := query.Get("X-Amz-Signature")
		if signature == "" {
			http.Error(w, "missing X-Amz-Signature", http.StatusBadRequest)
			return
		}

		if query.Get("X-Amz-SignedHeaders") != "host" {
			http.Error(w, "invalid or missing X-Amz-SignedHeaders", http.StatusBadRequest)
			return
		}

		date := query.Get("X-Amz-Date")
		signingTime, err := time.Parse("20060102T150405Z", date)
		if err != nil {
			http.Error(w, "invalid X-Amz-Date", http.StatusBadRequest)
			return
		}

		now := s.now().UTC()
		const maxClockSkew = 15 * time.Minute
		if signingTime.After(now.Add(maxClockSkew)) {
			http.Error(w, "request date is in the future", http.StatusForbidden)
			return
		}

		expires, err := strconv.ParseInt(query.Get("X-Amz-Expires"), 10, 64)
		if err != nil || expires <= 0 || expires > 604800 {
			http.Error(w, "invalid X-Amz-Expires", http.StatusBadRequest)
			return
		}

		if now.After(signingTime.Add(time.Duration(expires) * time.Second)) {
			http.Error(w, http.StatusText(http.StatusForbidden), http.StatusForbidden)
			return
		}

		credential := query.Get("X-Amz-Credential")
		parts := strings.Split(credential, "/")
		if len(parts) != 5 || parts[4] != "aws4_request" || parts[3] != "s3" || (len(date) >= 8 && parts[1] != date[:8]) {
			http.Error(w, "invalid X-Amz-Credential", http.StatusBadRequest)
			return
		}

		accessKey := parts[0]
		region := parts[2]
		if accessKey != s.cfg.SeaweedFS.S3AccessKey {
			http.Error(w, http.StatusText(http.StatusForbidden), http.StatusForbidden)
			return
		}

		u := *s.publicEndpoint
		u.Path = strings.TrimSuffix(s.publicEndpoint.Path, "/") + r.URL.Path
		if r.URL.RawPath != "" {
			u.RawPath = strings.TrimSuffix(s.publicEndpoint.EscapedPath(), "/") + r.URL.RawPath
		}
		query.Del("X-Amz-Signature")
		u.RawQuery = query.Encode()
		reconstructedReq, err := http.NewRequestWithContext(r.Context(), r.Method, u.String(), nil)
		if err != nil {
			http.Error(w, http.StatusText(http.StatusBadRequest), http.StatusBadRequest)
			return
		}

		signedURI, _, err := s.signer.PresignHTTP(
			r.Context(),
			aws.Credentials{
				AccessKeyID:     s.cfg.SeaweedFS.S3AccessKey,
				SecretAccessKey: s.cfg.SeaweedFS.S3SecretKey,
			},
			reconstructedReq,
			"UNSIGNED-PAYLOAD",
			"s3",
			region,
			signingTime,
		)
		if err != nil {
			http.Error(w, http.StatusText(http.StatusInternalServerError), http.StatusInternalServerError)
			return
		}

		signedURL, err := url.Parse(signedURI)
		if err != nil {
			http.Error(w, http.StatusText(http.StatusInternalServerError), http.StatusInternalServerError)
			return
		}

		got := []byte(signature)
		want := []byte(signedURL.Query().Get("X-Amz-Signature"))
		if subtle.ConstantTimeCompare(got, want) != 1 {
			http.Error(w, http.StatusText(http.StatusForbidden), http.StatusForbidden)
			return
		}
		next.ServeHTTP(w, r)
	})
}

func Run(cfg config.Config, filerClient *seaweedfs.FilerClient) error {
	router, err := NewRouter(cfg, filerClient)
	if err != nil {
		return fmt.Errorf("creating router: %w", err)
	}
	httpServer := &http.Server{
		Addr:         fmt.Sprintf(":%d", cfg.Port),
		Handler:      router,
		ReadTimeout:  cfg.ReadTimeout,
		WriteTimeout: cfg.WriteTimeout,
		IdleTimeout:  cfg.IdleTimeout,
	}

	return httpServer.ListenAndServe()
}
