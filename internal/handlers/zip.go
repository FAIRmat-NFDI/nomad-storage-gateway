package handlers

import (
	"net/http"

	config "github.com/FAIRmat-NFDI/nomad-storage-gateway/internal/config"
	"github.com/go-chi/chi/v5"
)

func ZipHandler(w http.ResponseWriter, r *http.Request, cfg config.Config) {
	uploadID := chi.URLParam(r, "upload_id")
	if uploadID == "" {
		http.Error(w, "missing id", http.StatusBadRequest)
		return
	}
	subpath := chi.URLParam(r, "*") // e.g. "input/INCAR" or "input"
	if subpath == "" {
		redirectUploadZip(w, r, uploadID, cfg)
	} else {
		// todo: handle redirects for zipped-subdirectories
	}
}

func redirectUploadZip(w http.ResponseWriter, r *http.Request, uploadID string, cfg config.Config) {

}
