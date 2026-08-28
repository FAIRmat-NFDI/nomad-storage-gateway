package server

import (
	"fmt"
	"net/http"

	config "github.com/FAIRmat-NFDI/nomad-storage-gateway/internal/config"
	seaweedfs "github.com/FAIRmat-NFDI/nomad-storage-gateway/internal/seaweedfs"
	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"
)

type Server struct {
	cfg         config.Config
	filerClient *seaweedfs.FilerClient
}

func NewRouter(cfg config.Config, filerClient *seaweedfs.FilerClient) http.Handler {
	s := &Server{cfg: cfg, filerClient: filerClient}
	r := chi.NewRouter()

	r.Use(middleware.RequestID)
	r.Use(middleware.ClientIPFromRemoteAddr)
	r.Use(middleware.Logger)
	r.Use(middleware.Recoverer)

	r.Get("/health", s.health)
	r.Get("/uploads/{upload_id}/*", s.zip)

	return r
}

func (s *Server) health(w http.ResponseWriter, r *http.Request) {
	w.Write([]byte("ok"))
}

func Run(cfg config.Config, filerClient *seaweedfs.FilerClient) error {
	httpServer := &http.Server{
		Addr:         fmt.Sprintf(":%d", cfg.Port),
		Handler:      NewRouter(cfg, filerClient),
		ReadTimeout:  cfg.ReadTimeout,
		WriteTimeout: cfg.WriteTimeout,
		IdleTimeout:  cfg.IdleTimeout,
	}

	return httpServer.ListenAndServe()
}
