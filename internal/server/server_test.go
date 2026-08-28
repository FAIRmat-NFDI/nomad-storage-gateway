package server

import (
	"net/http"
	"net/http/httptest"
	"testing"

	config "github.com/FAIRmat-NFDI/nomad-storage-gateway/internal/config"
)

func TestHealthEndpoint(t *testing.T) {
	cfg := config.Config{}

	req := httptest.NewRequest(http.MethodGet, "/health", nil)
	rec := httptest.NewRecorder()

	NewRouter(cfg, nil).ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected status 200, got %d", rec.Code)
	}

	if got := rec.Body.String(); got != "ok" {
		t.Fatalf("expected body %q, got %q", "ok", got)
	}
}
