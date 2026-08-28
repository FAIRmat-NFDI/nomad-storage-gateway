package server

import (
	"net/http"

	"github.com/go-chi/chi/v5"
)

func (s *Server) zip(w http.ResponseWriter, r *http.Request) {
	uploadID := chi.URLParam(r, "upload_id")
	if uploadID == "" {
		http.Error(w, "missing upload id", http.StatusBadRequest)
		return
	}

	subpath := chi.URLParam(r, "*")
	if subpath != "" {
		http.Error(w, "zipped subdirectories are not implemented", http.StatusNotImplemented)
		return
	}

	// The redirect implementation will use s.cfg and s.filerClient.
	http.Error(w, "zip redirect is not implemented", http.StatusNotImplemented)
}
