package server

import (
	"archive/zip"
	"errors"
	"fmt"
	"io"
	"mime"
	"net/http"
	"path"
	"strconv"
	"strings"

	"github.com/go-chi/chi/v5"
)

func (s *Server) file(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	uploadID := chi.URLParam(r, "upload_id")
	subpath := chi.URLParam(r, "*")
	if subpath == "" {
		http.Error(w, "invalid filename", http.StatusBadRequest)
		return
	}

	query := r.URL.Query()

	offsetValue := query.Get("offset")
	lengthValue := query.Get("length")

	offset := int64(0)
	length := int64(-1)
	var err error

	if offsetValue != "" {
		offset, err = strconv.ParseInt(offsetValue, 10, 64)
		if err != nil {
			http.Error(w, "invalid offset value", http.StatusBadRequest)
			return
		}
	}

	if lengthValue != "" {
		length, err = strconv.ParseInt(lengthValue, 10, 64)
		if err != nil {
			http.Error(w, "invalid length value", http.StatusBadRequest)
			return
		}
	}

	if offset < 0 {
		http.Error(w, "Invalid offset provided.", http.StatusBadRequest)
		return
	}
	if length <= 0 && length != -1 {
		http.Error(w, "Invalid length provided.", http.StatusBadRequest)
		return
	}

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
	s.streamFile(w, r, obj, subpath, offset, length)
}

func (s *Server) streamFile(w http.ResponseWriter, r *http.Request, obj *zipObject, subpath string, offset int64, length int64) {
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
	if !singleFile || len(matched) == 0 {
		http.Error(w, "invalid file path", http.StatusNotFound)
		return
	}

	src := matched[0]
	uncompressedSize := int64(src.UncompressedSize64)
	if offset > uncompressedSize {
		http.Error(w, "Invalid offset provided.", http.StatusBadRequest)
		return
	}

	var source io.Reader
	if src.Method == zip.Deflate {
		rc, err := src.Open()
		if err != nil {
			http.Error(w, "failed to open source file", http.StatusInternalServerError)
			return
		}
		defer rc.Close()
		source = rc
		if offset > 0 {
			if _, err := io.CopyN(io.Discard, source, offset); err != nil {
				http.Error(w, "Invalid offset provided.", http.StatusBadRequest)
				return
			}
		}
	} else {
		// OpenRaw returns *io.SectionReader which implements io.Seeker
		raw, err := src.OpenRaw()
		if err != nil {
			http.Error(w, "failed to open source file", http.StatusInternalServerError)
			return
		}
		if c, ok := raw.(io.Closer); ok {
			defer c.Close()
		}
		source = raw
		if offset > 0 {
			seeker, ok := source.(io.Seeker)
			if !ok {
				http.Error(w, "Invalid offset provided.", http.StatusBadRequest)
				return
			}
			if _, err := seeker.Seek(offset, io.SeekStart); err != nil {
				http.Error(w, "Invalid offset provided.", http.StatusBadRequest)
				return
			}
		}
	}

	output := io.Reader(source)
	if length >= 0 {
		output = io.LimitReader(source, length)
	}

	cleanPath := strings.ReplaceAll(strings.Trim(subpath, "/\\"), "\\", "/")
	filename := path.Base(cleanPath)
	contentType := "application/octet-stream"
	if offset == 0 && length == -1 {
		if extType := mime.TypeByExtension(path.Ext(filename)); extType != "" {
			contentType = extType
		}
	}
	w.Header().Set("Content-Type", contentType)
	w.Header().Set(
		"Content-Disposition",
		fmt.Sprintf(`attachment; filename="%s"`, filename),
	)

	_, _ = io.Copy(w, output)
}
