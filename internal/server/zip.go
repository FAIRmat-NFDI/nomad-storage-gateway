package server

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"path"
	"strings"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/go-chi/chi/v5"
	"github.com/seaweedfs/seaweedfs/weed/pb/filer_pb"
	"google.golang.org/grpc"
)

type filerLookupClient interface {
	LookupDirectoryEntry(
		context.Context,
		*filer_pb.LookupDirectoryEntryRequest,
		...grpc.CallOption,
	) (*filer_pb.LookupDirectoryEntryResponse, error)
}

func (s *Server) zip(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	uploadID := chi.URLParam(r, "upload_id")
	obj, err := s.resolveZipObject(ctx, uploadID)
	if err != nil {
		var resolveErr *zipResolveError
		if errors.As(err, &resolveErr) {
			http.Error(w, resolveErr.message, resolveErr.status)
		} else {
			http.Error(w, "internal error", http.StatusInternalServerError)
		}
		return
	}

	subpath := chi.URLParam(r, "*")
	if subpath == "" {
		s.redirectUploadZip(w, r, obj)
		return
	}
	s.streamSubdirZip(w, r, obj, subpath)
}

func (s *Server) streamSubdirZip(w http.ResponseWriter, r *http.Request, obj *zipObject, subpath string) {
	if subpath == "" {
		http.Error(w, "invalid subpath", http.StatusBadRequest)
		return
	}
	if obj.size <= 0 {
		http.Error(w, "invalid zip size response", http.StatusBadGateway)
		return
	}

	zipReader, err := s.getOrLoadZipReader(obj)
	if err != nil {
		http.Error(w, "failed to open zip", http.StatusBadGateway)
		return
	}

	matched, singleFile := matchZipFiles(zipReader.File, subpath)
	if len(matched) == 0 {
		http.Error(w, "path not found", http.StatusNotFound)
		return
	}

	cleanPath := strings.ReplaceAll(strings.Trim(subpath, "/\\"), "\\", "/")
	w.Header().Set("Content-Type", "application/zip")
	w.Header().Set(
		"Content-Disposition",
		fmt.Sprintf(`attachment; filename="%s.zip"`, path.Base(cleanPath)),
	)
	_ = writeZipSubset(w, matched, subpath, singleFile)
}

func (s *Server) redirectUploadZip(w http.ResponseWriter, r *http.Request, obj *zipObject) {
	presigner := s3.NewPresignClient(obj.client)
	presignedURL, err := presigner.PresignGetObject(
		r.Context(),
		&s3.GetObjectInput{Bucket: aws.String(obj.bucket), Key: aws.String(obj.key)},
	)
	if err != nil {
		http.Error(w, fmt.Sprintf("presign failed: %v", err), http.StatusInternalServerError)
		return
	}
	http.Redirect(w, r, presignedURL.URL, http.StatusTemporaryRedirect)
}
