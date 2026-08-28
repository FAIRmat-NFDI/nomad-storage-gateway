package internal

import (
	"fmt"
	"net/http"

	config "github.com/FAIRmat-NFDI/nomad-storage-gateway/internal/config"
	handlers "github.com/FAIRmat-NFDI/nomad-storage-gateway/internal/handlers"
	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"
)

type Server struct {
	cfg config.Config
}

func NewRouter(cfg config.Config) http.Handler {
	s := &Server{cfg: cfg}
	r := chi.NewRouter()

	r.Use(middleware.RequestID)
	r.Use(middleware.ClientIPFromRemoteAddr)
	r.Use(middleware.Logger)
	r.Use(middleware.Recoverer)

	r.Get("/health", s.health)
	r.Get("/zip", s.zip)

	return r
}

func (s *Server) health(w http.ResponseWriter, r *http.Request) {
	w.Write([]byte("ok"))
}

func (s *Server) zip(w http.ResponseWriter, r *http.Request) {
	handlers.ZipHandler(w, r, s.cfg)
}

func Run(cfg config.Config) error {
	httpServer := &http.Server{
		Addr:         fmt.Sprintf(":%d", cfg.Port),
		Handler:      NewRouter(cfg),
		ReadTimeout:  cfg.ReadTimeout,
		WriteTimeout: cfg.WriteTimeout,
		IdleTimeout:  cfg.IdleTimeout,
	}

	return httpServer.ListenAndServe()
}
