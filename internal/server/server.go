package server

import (
	"context"
	"fmt"
	"maps"
	"net/http"

	config "github.com/FAIRmat-NFDI/nomad-storage-gateway/internal/config"
	seaweedfs "github.com/FAIRmat-NFDI/nomad-storage-gateway/internal/seaweedfs"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"
)

type Server struct {
	cfg         config.Config
	filerClient filerLookupClient
	presigners  map[string]*s3.PresignClient
}

// centralSeaweedFSProvider is reserved for the gateway's internal SeaweedFS store.
const centralSeaweedFSProvider = "central_seaweedfs"

func NewRouter(cfg config.Config, filerClient filerLookupClient) (http.Handler, error) {
	if _, ok := cfg.Providers[centralSeaweedFSProvider]; ok {
		return nil, fmt.Errorf("provider name %q is reserved", centralSeaweedFSProvider)
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

	s := &Server{cfg: cfg, filerClient: filerClient, presigners: presigners}
	r := chi.NewRouter()

	r.Use(middleware.RequestID)
	r.Use(middleware.ClientIPFromRemoteAddr)
	r.Use(middleware.Logger)
	r.Use(middleware.Recoverer)

	r.Get("/health", s.health)
	r.Get("/zip/{upload_id}", s.zip)
	r.Get("/zip/{upload_id}/*", s.zip)

	return r, nil
}

func (s *Server) health(w http.ResponseWriter, r *http.Request) {
	w.Write([]byte("ok"))
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
