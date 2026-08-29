package server

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/FAIRmat-NFDI/nomad-storage-gateway/internal/config"
)

func TestHealthEndpoint(t *testing.T) {
	cfg := config.Config{}

	req := httptest.NewRequest(http.MethodGet, "/health", nil)
	rec := httptest.NewRecorder()
	router, err := NewRouter(cfg, nil)
	if err != nil {
		t.Fatalf("NewRouter() error = %v", err)
	}

	router.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected status 200, got %d", rec.Code)
	}

	if got := rec.Body.String(); got != "ok" {
		t.Fatalf("expected body %q, got %q", "ok", got)
	}
}

func TestNewRouterWithNoProviders(t *testing.T) {
	if _, err := NewRouter(config.Config{}, nil); err != nil {
		t.Fatalf("NewRouter() error = %v", err)
	}
}

func TestNewRouterDoesNotMutateProviders(t *testing.T) {
	cfg := testConfig()
	if _, err := NewRouter(cfg, nil); err != nil {
		t.Fatalf("NewRouter() error = %v", err)
	}

	if _, ok := cfg.Providers[centralSeaweedFSProvider]; ok {
		t.Fatal(`NewRouter() added the internal provider to cfg.Providers`)
	}
}

func TestNewRouterRejectsReservedProviderName(t *testing.T) {
	cfg := testConfig()
	cfg.Providers[centralSeaweedFSProvider] = config.ObjectStore{}

	if _, err := NewRouter(cfg, nil); err == nil {
		t.Fatalf("NewRouter() error = nil, want reserved provider error")
	}
}
