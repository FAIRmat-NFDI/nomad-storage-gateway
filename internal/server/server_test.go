package server

import (
	"net/http"
	"net/http/httptest"
	"testing"

	config "github.com/FAIRmat-NFDI/nomad-storage-gateway/internal/config"
	seaweedfs "github.com/FAIRmat-NFDI/nomad-storage-gateway/internal/seaweedfs"
)

func TestHealthEndpoint(t *testing.T) {
	cfg := config.Config{}

	req := httptest.NewRequest(http.MethodGet, "/health", nil)
	rec := httptest.NewRecorder()
	router, err := NewRouter(cfg, nil)
	if err != nil {
		t.Fatalf("Failed to create a router %v", err)
	}

	router.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected status 200, got %d", rec.Code)
	}

	if got := rec.Body.String(); got != "ok" {
		t.Fatalf("expected body %q, got %q", "ok", got)
	}
}

func TestZipEndpoint(t *testing.T) {
	cfg := config.Config{
		SeaweedFS: config.SeaweedFSConfig{
			S3Bucket:      "nomad-public",
			FilerEndpoint: "localhost:18888",
		},
	}

	fc, err := seaweedfs.NewFilerClient(cfg, nil)
	if err != nil {
		t.Fatalf("failed to connect to filer: %v", err)
	}
	defer fc.Close()

	router, err := NewRouter(cfg, fc)
	if err != nil {
		t.Fatalf("Failed to create a router %v", err)
	}

	t.Run("non_existent_upload", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodGet, "/zip/non_existent_upload", nil)
		rec := httptest.NewRecorder()
		router.ServeHTTP(rec, req)

		if rec.Code != http.StatusNotFound {
			t.Fatalf("expected status 404, got %d", rec.Code)
		}
	})

	t.Run("local_upload_lookup", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodGet, "/zip/nomad_local_upload", nil)
		rec := httptest.NewRecorder()
		router.ServeHTTP(rec, req)

		if rec.Code != http.StatusNotImplemented {
			t.Fatalf("expected status 501 (Not Implemented), got %d: %s", rec.Code, rec.Body.String())
		}
	})

	t.Run("cloud_upload_lookup", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodGet, "/zip/nomad_cloud_upload", nil)
		rec := httptest.NewRecorder()
		router.ServeHTTP(rec, req)

		if rec.Code != http.StatusNotImplemented {
			t.Fatalf("expected status 501 (Not Implemented), got %d: %s", rec.Code, rec.Body.String())
		}
	})
}
