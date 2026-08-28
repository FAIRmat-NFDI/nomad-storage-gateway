package internal

import (
	"net/http"

	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"
)

func NewRouter() http.Handler {
	r := chi.NewRouter()

	r.Use(middleware.RequestID)
	r.Use(middleware.ClientIPFromRemoteAddr)
	r.Use(middleware.Logger)
	r.Use(middleware.Recoverer)

	r.Get("/health", health)

	return r
}

func health(w http.ResponseWriter, r *http.Request) {
	w.Write([]byte("ok"))
}

func Server() {
	http.ListenAndServe(":3333", NewRouter())
}
