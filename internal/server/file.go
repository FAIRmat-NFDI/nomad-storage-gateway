package server

import (
	"archive/zip"
	"compress/gzip"
	"errors"
	"fmt"
	"io"
	"mime"
	"net/http"
	"path"
	"strconv"
	"strings"

	"github.com/go-chi/chi/v5"
	"github.com/ulikunitz/xz"
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

	decompress, err := parseBoolQuery(query.Get("decompress"))
	if err != nil {
		http.Error(w, "invalid decompress value", http.StatusBadRequest)
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
	s.streamFile(w, r, obj, subpath, offset, length, decompress)
}

func parseBoolQuery(value string) (bool, error) {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "", "0", "false", "no", "off":
		return false, nil
	case "1", "true", "yes", "on":
		return true, nil
	default:
		return false, fmt.Errorf("invalid bool %q", value)
	}
}

func (s *Server) streamFile(w http.ResponseWriter, r *http.Request, obj *zipObject, subpath string, offset int64, length int64, decompress bool) {
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

	cleanPath := strings.ReplaceAll(strings.Trim(subpath, "/\\"), "\\", "/")
	decompressMember := decompress && isDecompressiblePath(cleanPath)

	src := matched[0]
	if !decompressMember && offset > int64(src.UncompressedSize64) {
		http.Error(w, "Invalid offset provided.", http.StatusBadRequest)
		return
	}

	source, closeMember, err := openZipMember(src)
	if err != nil {
		http.Error(w, "failed to open source file", http.StatusInternalServerError)
		return
	}
	defer closeMember()

	if decompressMember {
		decoded, closeDecoded, err := wrapDecompress(source, cleanPath)
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		defer closeDecoded()
		source = decoded
	}

	if offset > 0 {
		if seeker, ok := source.(io.Seeker); ok {
			if _, err := seeker.Seek(offset, io.SeekStart); err != nil {
				http.Error(w, "Invalid offset provided.", http.StatusBadRequest)
				return
			}
		} else if _, err := io.CopyN(io.Discard, source, offset); err != nil {
			http.Error(w, "Invalid offset provided.", http.StatusBadRequest)
			return
		}
	}

	output := io.Reader(source)
	if length >= 0 {
		output = io.LimitReader(source, length)
	}

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

func isDecompressiblePath(cleanPath string) bool {
	return strings.HasSuffix(cleanPath, ".gz") || strings.HasSuffix(cleanPath, ".xz")
}

func wrapDecompress(source io.Reader, cleanPath string) (io.Reader, func(), error) {
	switch {
	case strings.HasSuffix(cleanPath, ".gz"):
		gz, err := gzip.NewReader(source)
		if err != nil {
			return nil, nil, fmt.Errorf("failed to decompress gzip")
		}
		return gz, func() { _ = gz.Close() }, nil
	case strings.HasSuffix(cleanPath, ".xz"):
		r, err := xz.NewReader(source)
		if err != nil {
			return nil, nil, fmt.Errorf("failed to decompress xz")
		}
		return r, func() {}, nil
	default:
		return source, func() {}, nil
	}
}

func openZipMember(src *zip.File) (io.Reader, func(), error) {
	if src.Method == zip.Deflate {
		rc, err := src.Open()
		if err != nil {
			return nil, nil, err
		}
		return rc, func() { _ = rc.Close() }, nil
	}

	// OpenRaw returns *io.SectionReader which implements io.Seeker
	raw, err := src.OpenRaw()
	if err != nil {
		return nil, nil, err
	}
	closeFn := func() {}
	if c, ok := raw.(io.Closer); ok {
		closeFn = func() { _ = c.Close() }
	}
	return raw, closeFn, nil
}
