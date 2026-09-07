package server

import (
	"archive/zip"
	"bytes"
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/FAIRmat-NFDI/nomad-storage-gateway/internal/config"
	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/credentials"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/seaweedfs/seaweedfs/weed/pb/filer_pb"
)

type rangeRequest struct {
	rangeHeader string
}

func newRangeTestServer(t *testing.T, data []byte) (*httptest.Server, *[]rangeRequest) {
	t.Helper()

	var requests []rangeRequest
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests = append(requests, rangeRequest{rangeHeader: r.Header.Get("Range")})
		if r.Context().Err() != nil {
			http.Error(w, r.Context().Err().Error(), http.StatusRequestTimeout)
			return
		}

		rangeHeader := r.Header.Get("Range")
		if !strings.HasPrefix(rangeHeader, "bytes=") {
			http.Error(w, "missing range", http.StatusBadRequest)
			return
		}
		parts := strings.Split(strings.TrimPrefix(rangeHeader, "bytes="), "-")
		if len(parts) != 2 {
			http.Error(w, "invalid range", http.StatusBadRequest)
			return
		}
		start, err := strconv.ParseInt(parts[0], 10, 64)
		if err != nil {
			http.Error(w, "invalid range start", http.StatusBadRequest)
			return
		}
		end, err := strconv.ParseInt(parts[1], 10, 64)
		if err != nil {
			http.Error(w, "invalid range end", http.StatusBadRequest)
			return
		}
		if start < 0 || end >= int64(len(data)) || start > end {
			http.Error(w, "range out of bounds", http.StatusRequestedRangeNotSatisfiable)
			return
		}

		w.Header().Set("Content-Length", fmt.Sprintf("%d", end-start+1))
		w.WriteHeader(http.StatusPartialContent)
		_, _ = w.Write(data[start : end+1])
	}))
	t.Cleanup(srv.Close)
	return srv, &requests
}

func newTestS3Client(t *testing.T, endpoint string) *s3.Client {
	t.Helper()

	client := s3.NewFromConfig(aws.Config{
		Region: "us-east-1",
		Credentials: credentials.NewStaticCredentialsProvider(
			"test-access",
			"test-secret",
			"",
		),
	}, func(o *s3.Options) {
		o.BaseEndpoint = aws.String(endpoint)
		o.UsePathStyle = true
	})
	return client
}

func TestZipObjectReaderReadAt(t *testing.T) {
	fixture := []byte("0123456789abcdef")
	srv, requests := newRangeTestServer(t, fixture)
	client := newTestS3Client(t, srv.URL)

	reader := newZipObjectReader(context.Background(), client, "bucket", "object.zip")
	buf := make([]byte, 4)
	n, err := reader.ReadAt(buf, 5)
	if err != nil {
		t.Fatalf("ReadAt() error = %v", err)
	}
	if n != 4 {
		t.Fatalf("ReadAt() n = %d, want 4", n)
	}
	if string(buf) != "5678" {
		t.Fatalf("ReadAt() = %q, want %q", string(buf), "5678")
	}
	if len(*requests) != 1 {
		t.Fatalf("GetObject calls = %d, want 1", len(*requests))
	}
	if (*requests)[0].rangeHeader != "bytes=5-8" {
		t.Errorf("Range = %q, want bytes=5-8", (*requests)[0].rangeHeader)
	}
}

func TestZipObjectReaderForwardsContext(t *testing.T) {
	fixture := []byte("0123456789")
	srv, _ := newRangeTestServer(t, fixture)
	client := newTestS3Client(t, srv.URL)

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	reader := newZipObjectReader(ctx, client, "bucket", "object.zip")
	buf := make([]byte, 4)
	_, err := reader.ReadAt(buf, 0)
	if err == nil {
		t.Fatal("ReadAt() error = nil, want context cancellation error")
	}
}

func TestZipObjectReaderEmptyRead(t *testing.T) {
	srv, requests := newRangeTestServer(t, []byte("data"))
	client := newTestS3Client(t, srv.URL)

	reader := newZipObjectReader(context.Background(), client, "bucket", "object.zip")
	n, err := reader.ReadAt(nil, 0)
	if err != nil {
		t.Fatalf("ReadAt() error = %v", err)
	}
	if n != 0 {
		t.Fatalf("ReadAt() n = %d, want 0", n)
	}
	if len(*requests) != 0 {
		t.Fatalf("GetObject calls = %d, want 0", len(*requests))
	}
}

func TestZipObjectReaderInvalidOffset(t *testing.T) {
	client := newTestS3Client(t, "http://unused.test")
	reader := newZipObjectReader(context.Background(), client, "bucket", "object.zip")
	_, err := reader.ReadAt(make([]byte, 1), -1)
	if err == nil {
		t.Fatal("ReadAt() error = nil, want invalid offset error")
	}
}

func newZipFixtureServer(t *testing.T, zipData []byte) *httptest.Server {
	t.Helper()

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Context().Err() != nil {
			http.Error(w, r.Context().Err().Error(), http.StatusRequestTimeout)
			return
		}
		rangeHeader := r.Header.Get("Range")
		if rangeHeader == "" {
			w.Header().Set("Content-Length", fmt.Sprintf("%d", len(zipData)))
			_, _ = w.Write(zipData)
			return
		}
		parts := strings.Split(strings.TrimPrefix(rangeHeader, "bytes="), "-")
		start, _ := strconv.ParseInt(parts[0], 10, 64)
		end, _ := strconv.ParseInt(parts[1], 10, 64)
		w.Header().Set("Content-Length", fmt.Sprintf("%d", end-start+1))
		w.WriteHeader(http.StatusPartialContent)
		_, _ = w.Write(zipData[start : end+1])
	}))
	t.Cleanup(srv.Close)
	return srv
}

func testConfigWithEndpoint(endpoint string) config.Config {
	cfg := testConfig()
	cfg.SeaweedFS.S3Endpoint = endpoint
	cfg.SeaweedFS.PublicEndpoint = endpoint
	return cfg
}

func newZipStreamingRouter(t *testing.T, zipData []byte) (http.Handler, config.Config) {
	t.Helper()

	srv := newZipFixtureServer(t, zipData)
	cfg := testConfigWithEndpoint(srv.URL)
	filer := &fakeFilerClient{response: &filer_pb.LookupDirectoryEntryResponse{
		Entry: &filer_pb.Entry{
			Name:       "raw-public.plain.zip",
			Attributes: &filer_pb.FuseAttributes{FileSize: uint64(len(zipData))},
		},
	}}

	router, err := NewRouter(cfg, filer)
	if err != nil {
		t.Fatalf("NewRouter() error = %v", err)
	}
	return router, cfg
}

func readResponseZip(t *testing.T, body []byte) *zip.Reader {
	t.Helper()

	reader, err := zip.NewReader(bytes.NewReader(body), int64(len(body)))
	if err != nil {
		t.Fatalf("open response zip: %v", err)
	}
	return reader
}

func TestZipEndpointSubpathStreamsDirectory(t *testing.T) {
	const uploadID = "abcdef"
	zipData := buildTestZip(t, map[string]zipMethod{
		"vasp/OUTCAR": zipDeflate,
		"vasp/INCAR":  zipStore,
	})

	router, cfg := newZipStreamingRouter(t, zipData)
	req := signTestRequest(t, cfg, http.MethodGet, "/zip/"+uploadID+"/vasp", time.Now().UTC(), 15*time.Minute)
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d; body = %q", rec.Code, http.StatusOK, rec.Body.String())
	}
	if got := rec.Header().Get("Content-Type"); got != "application/zip" {
		t.Fatalf("Content-Type = %q, want application/zip", got)
	}
	if got := rec.Header().Get("Content-Disposition"); !strings.Contains(got, `filename="vasp.zip"`) {
		t.Fatalf("Content-Disposition = %q, want filename=\"vasp.zip\"", got)
	}

	out := readResponseZip(t, rec.Body.Bytes())
	if len(out.File) != 2 {
		t.Fatalf("output entries = %d, want 2", len(out.File))
	}
	contents := zipContentsByName(t, out.File)
	if _, ok := contents["OUTCAR"]; !ok {
		t.Fatalf("output names = %v, want OUTCAR", contents)
	}
	if _, ok := contents["INCAR"]; !ok {
		t.Fatalf("output names = %v, want INCAR", contents)
	}
	for name := range contents {
		if strings.HasPrefix(name, "vasp/") {
			t.Fatalf("output name %q still has vasp/ prefix", name)
		}
	}
	if contents["OUTCAR"] != "content-vasp/OUTCAR" {
		t.Errorf("OUTCAR content = %q, want %q", contents["OUTCAR"], "content-vasp/OUTCAR")
	}
	if contents["INCAR"] != "content-vasp/INCAR" {
		t.Errorf("INCAR content = %q, want %q", contents["INCAR"], "content-vasp/INCAR")
	}
}

func TestZipEndpointSubpathStreamsSingleFile(t *testing.T) {
	const uploadID = "abcdef"
	zipData := buildTestZip(t, map[string]zipMethod{
		"vasp/OUTCAR": zipDeflate,
		"vasp/INCAR":  zipStore,
	})

	router, cfg := newZipStreamingRouter(t, zipData)
	req := signTestRequest(t, cfg, http.MethodGet, "/zip/"+uploadID+"/vasp/OUTCAR", time.Now().UTC(), 15*time.Minute)
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d; body = %q", rec.Code, http.StatusOK, rec.Body.String())
	}
	if got := rec.Header().Get("Content-Type"); got != "application/zip" {
		t.Fatalf("Content-Type = %q, want application/zip", got)
	}
	if got := rec.Header().Get("Content-Disposition"); !strings.Contains(got, `filename="OUTCAR.zip"`) {
		t.Fatalf("Content-Disposition = %q, want filename=\"OUTCAR.zip\"", got)
	}

	out := readResponseZip(t, rec.Body.Bytes())
	if len(out.File) != 1 {
		t.Fatalf("output entries = %d, want 1", len(out.File))
	}
	if out.File[0].Name != "OUTCAR" {
		t.Fatalf("output name = %q, want OUTCAR", out.File[0].Name)
	}
	if got := readZipFileContent(t, out.File[0]); got != "content-vasp/OUTCAR" {
		t.Errorf("OUTCAR content = %q, want %q", got, "content-vasp/OUTCAR")
	}
}

func TestZipEndpointSubpathNotFound(t *testing.T) {
	const uploadID = "abcdef"
	zipData := buildTestZip(t, map[string]zipMethod{
		"input/INCAR": zipDeflate,
	})

	router, cfg := newZipStreamingRouter(t, zipData)

	req := signTestRequest(t, cfg, http.MethodGet, "/zip/"+uploadID+"/missing/INCAR", time.Now().UTC(), 15*time.Minute)
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)

	if rec.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want %d; body = %q", rec.Code, http.StatusNotFound, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), "path not found") {
		t.Fatalf("body = %q, want it to contain %q", rec.Body.String(), "path not found")
	}
}
