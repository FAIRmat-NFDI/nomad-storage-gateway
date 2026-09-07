package server

import (
	"archive/zip"
	"bytes"
	"compress/flate"
	"compress/gzip"
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/FAIRmat-NFDI/nomad-storage-gateway/internal/config"
)

const fileTestUploadID = "abcdef"

func defaultFileZip(t *testing.T) []byte {
	t.Helper()
	return buildTestZip(t, map[string]zipMethod{
		"vasp/OUTCAR":     zipDeflate,
		"vasp/INCAR":      zipStore,
		"data/notes.json": zipStore,
	})
}

func serveFile(t *testing.T, router http.Handler, cfg config.Config, path string, extraQuery url.Values) *httptest.ResponseRecorder {
	t.Helper()

	req := signTestRequest(t, cfg, http.MethodGet, path, time.Now().UTC(), 15*time.Minute, extraQuery)
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)
	return rec
}

func TestFileEndpointStreamsDeflateMember(t *testing.T) {
	zipData := defaultFileZip(t)
	_, router, cfg := newZipStreamingRouter(t, zipData)

	rec := serveFile(t, router, cfg, "/file/"+fileTestUploadID+"/vasp/OUTCAR", nil)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d; body = %q", rec.Code, http.StatusOK, rec.Body.String())
	}
	if got := rec.Header().Get("Content-Type"); got != "application/octet-stream" {
		t.Fatalf("Content-Type = %q, want application/octet-stream", got)
	}
	if got := rec.Header().Get("Content-Disposition"); !strings.Contains(got, `filename="OUTCAR"`) {
		t.Fatalf("Content-Disposition = %q, want filename=\"OUTCAR\"", got)
	}
	if got := rec.Body.String(); got != "content-vasp/OUTCAR" {
		t.Fatalf("body = %q, want %q", got, "content-vasp/OUTCAR")
	}
}

func TestFileEndpointStreamsStoreMember(t *testing.T) {
	zipData := defaultFileZip(t)
	_, router, cfg := newZipStreamingRouter(t, zipData)

	rec := serveFile(t, router, cfg, "/file/"+fileTestUploadID+"/vasp/INCAR", nil)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d; body = %q", rec.Code, http.StatusOK, rec.Body.String())
	}
	if got := rec.Body.String(); got != "content-vasp/INCAR" {
		t.Fatalf("body = %q, want %q", got, "content-vasp/INCAR")
	}
}

func TestFileEndpointInvalidPaths(t *testing.T) {
	zipData := defaultFileZip(t)
	_, router, cfg := newZipStreamingRouter(t, zipData)

	tests := []struct {
		name string
		path string
	}{
		{name: "directory", path: "/file/" + fileTestUploadID + "/vasp"},
		{name: "missing member", path: "/file/" + fileTestUploadID + "/missing/INCAR"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			rec := serveFile(t, router, cfg, tt.path, nil)
			if rec.Code != http.StatusNotFound {
				t.Fatalf("status = %d, want %d; body = %q", rec.Code, http.StatusNotFound, rec.Body.String())
			}
			if !strings.Contains(rec.Body.String(), "invalid file path") {
				t.Fatalf("body = %q, want it to contain %q", rec.Body.String(), "invalid file path")
			}
		})
	}
}

func TestFileEndpointOffsetAndLength(t *testing.T) {
	zipData := defaultFileZip(t)
	_, router, cfg := newZipStreamingRouter(t, zipData)
	wantSlice := "vasp"

	tests := []struct {
		name string
		path string
	}{
		{name: "deflate member", path: "/file/" + fileTestUploadID + "/vasp/OUTCAR"},
		{name: "store member", path: "/file/" + fileTestUploadID + "/vasp/INCAR"},
	}

	query := url.Values{
		"offset": {"8"},
		"length": {"4"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			rec := serveFile(t, router, cfg, tt.path, query)
			if rec.Code != http.StatusOK {
				t.Fatalf("status = %d, want %d; body = %q", rec.Code, http.StatusOK, rec.Body.String())
			}
			if got := rec.Body.String(); got != wantSlice {
				t.Fatalf("body = %q, want %q", got, wantSlice)
			}
		})
	}
}

func TestFileEndpointDeflateOffsetReturnsDecompressedBytes(t *testing.T) {
	zipData := defaultFileZip(t)
	_, router, cfg := newZipStreamingRouter(t, zipData)

	rec := serveFile(t, router, cfg, "/file/"+fileTestUploadID+"/vasp/OUTCAR", url.Values{
		"offset": {"0"},
		"length": {"7"},
	})

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d; body = %q", rec.Code, http.StatusOK, rec.Body.String())
	}
	if got := rec.Body.String(); got != "content" {
		t.Fatalf("body = %q, want decompressed prefix %q", got, "content")
	}
}

func TestFileEndpointOffsetLengthValidation(t *testing.T) {
	zipData := defaultFileZip(t)
	_, router, cfg := newZipStreamingRouter(t, zipData)
	basePath := "/file/" + fileTestUploadID + "/vasp/OUTCAR"

	tests := []struct {
		name         string
		query        url.Values
		wantStatus   int
		wantBodyPart string
		wantBody     string
	}{
		{
			name:         "negative offset",
			query:        url.Values{"offset": {"-1"}},
			wantStatus:   http.StatusBadRequest,
			wantBodyPart: "Invalid offset provided.",
		},
		{
			name:         "zero length",
			query:        url.Values{"length": {"0"}},
			wantStatus:   http.StatusBadRequest,
			wantBodyPart: "Invalid length provided.",
		},
		{
			name:         "length below minus one",
			query:        url.Values{"length": {"-2"}},
			wantStatus:   http.StatusBadRequest,
			wantBodyPart: "Invalid length provided.",
		},
		{
			name:       "length minus one is valid",
			query:      url.Values{"length": {"-1"}},
			wantStatus: http.StatusOK,
		},
		{
			name:         "unparseable offset",
			query:        url.Values{"offset": {"abc"}},
			wantStatus:   http.StatusBadRequest,
			wantBodyPart: "invalid offset value",
		},
		{
			name:         "unparseable length",
			query:        url.Values{"length": {"nope"}},
			wantStatus:   http.StatusBadRequest,
			wantBodyPart: "invalid length value",
		},
		{
			name:         "offset past eof",
			query:        url.Values{"offset": {"1000"}},
			wantStatus:   http.StatusBadRequest,
			wantBodyPart: "Invalid offset provided.",
		},
		{
			name:       "length past eof returns remainder",
			query:      url.Values{"offset": {"8"}, "length": {"1000"}},
			wantStatus: http.StatusOK,
			wantBody:   "vasp/OUTCAR",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			rec := serveFile(t, router, cfg, basePath, tt.query)
			if rec.Code != tt.wantStatus {
				t.Fatalf("status = %d, want %d; body = %q", rec.Code, tt.wantStatus, rec.Body.String())
			}
			if tt.wantBodyPart != "" && !strings.Contains(rec.Body.String(), tt.wantBodyPart) {
				t.Fatalf("body = %q, want it to contain %q", rec.Body.String(), tt.wantBodyPart)
			}
			if tt.wantBody != "" && rec.Body.String() != tt.wantBody {
				t.Fatalf("body = %q, want %q", rec.Body.String(), tt.wantBody)
			}
		})
	}
}

func TestFileEndpointMIMEType(t *testing.T) {
	zipData := defaultFileZip(t)
	_, router, cfg := newZipStreamingRouter(t, zipData)
	path := "/file/" + fileTestUploadID + "/data/notes.json"

	rec := serveFile(t, router, cfg, path, nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("full read status = %d, want %d", rec.Code, http.StatusOK)
	}
	if got := rec.Header().Get("Content-Type"); got != "application/json" {
		t.Fatalf("full read Content-Type = %q, want application/json", got)
	}

	rec = serveFile(t, router, cfg, path, url.Values{"offset": {"0"}, "length": {"5"}})
	if rec.Code != http.StatusOK {
		t.Fatalf("partial read status = %d, want %d", rec.Code, http.StatusOK)
	}
	if got := rec.Header().Get("Content-Type"); got != "application/octet-stream" {
		t.Fatalf("partial read Content-Type = %q, want application/octet-stream", got)
	}
}

func TestGetOrLoadZipReaderUsesCache(t *testing.T) {
	zipData := defaultFileZip(t)
	server, router, cfg := newZipStreamingRouter(t, zipData)

	obj, err := server.resolveZipObject(context.Background(), fileTestUploadID)
	if err != nil {
		t.Fatalf("resolveZipObject() error = %v", err)
	}

	reader1, err := server.getOrLoadZipReader(obj)
	if err != nil {
		t.Fatalf("first getOrLoadZipReader() error = %v", err)
	}
	reader2, err := server.getOrLoadZipReader(obj)
	if err != nil {
		t.Fatalf("second getOrLoadZipReader() error = %v", err)
	}
	if reader1 != reader2 {
		t.Fatal("expected cached *zip.Reader pointer equality")
	}

	rec := serveFile(t, router, cfg, "/file/"+fileTestUploadID+"/vasp/INCAR", nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("second member status = %d, want %d; body = %q", rec.Code, http.StatusOK, rec.Body.String())
	}
	if got := rec.Body.String(); got != "content-vasp/INCAR" {
		t.Fatalf("second member body = %q, want %q", got, "content-vasp/INCAR")
	}
}

func TestGetOrLoadZipReaderConcurrentLoadsShareReader(t *testing.T) {
	zipData := defaultFileZip(t)
	server, _, _ := newZipStreamingRouter(t, zipData)

	obj, err := server.resolveZipObject(context.Background(), fileTestUploadID)
	if err != nil {
		t.Fatalf("resolveZipObject() error = %v", err)
	}

	const workers = 8
	results := make(chan *zip.Reader, workers)
	errs := make(chan error, workers)
	for i := 0; i < workers; i++ {
		go func() {
			reader, err := server.getOrLoadZipReader(obj)
			if err != nil {
				errs <- err
				return
			}
			results <- reader
		}()
	}

	var first *zip.Reader
	for i := 0; i < workers; i++ {
		select {
		case err := <-errs:
			t.Fatalf("getOrLoadZipReader() error = %v", err)
		case reader := <-results:
			if first == nil {
				first = reader
				continue
			}
			if reader != first {
				t.Fatal("expected concurrent loads to share the same *zip.Reader")
			}
		}
	}
}

func gzipBytes(t *testing.T, data []byte) []byte {
	t.Helper()

	var buf bytes.Buffer
	writer := gzip.NewWriter(&buf)
	if _, err := writer.Write(data); err != nil {
		t.Fatalf("gzip write: %v", err)
	}
	if err := writer.Close(); err != nil {
		t.Fatalf("gzip close: %v", err)
	}
	return buf.Bytes()
}

func buildZipWithNamedBytes(t *testing.T, name string, data []byte, method zipMethod) []byte {
	t.Helper()

	var buf bytes.Buffer
	w := zip.NewWriter(&buf)
	w.RegisterCompressor(zip.Deflate, func(out io.Writer) (io.WriteCloser, error) {
		return flate.NewWriter(out, flate.BestCompression)
	})
	header := &zip.FileHeader{Name: name, Method: zip.Store}
	if method == zipDeflate {
		header.Method = zip.Deflate
	}
	fw, err := w.CreateHeader(header)
	if err != nil {
		t.Fatalf("create zip entry %q: %v", name, err)
	}
	if _, err := fw.Write(data); err != nil {
		t.Fatalf("write zip entry %q: %v", name, err)
	}
	if err := w.Close(); err != nil {
		t.Fatalf("close zip: %v", err)
	}
	return buf.Bytes()
}

func TestFileEndpointGzipDecompress(t *testing.T) {
	plain := []byte("hello gzip payload")
	gzPayload := gzipBytes(t, plain)
	zipData := buildZipWithNamedBytes(t, "data/notes.json.gz", gzPayload, zipStore)
	_, router, cfg := newZipStreamingRouter(t, zipData)
	path := "/file/" + fileTestUploadID + "/data/notes.json.gz"

	t.Run("decompress true returns gunzipped bytes", func(t *testing.T) {
		rec := serveFile(t, router, cfg, path, url.Values{"decompress": {"true"}})
		if rec.Code != http.StatusOK {
			t.Fatalf("status = %d, want %d; body = %q", rec.Code, http.StatusOK, rec.Body.String())
		}
		if got := rec.Body.String(); got != string(plain) {
			t.Fatalf("body = %q, want %q", got, plain)
		}
		if got := rec.Header().Get("Content-Disposition"); !strings.Contains(got, `filename="notes.json.gz"`) {
			t.Fatalf("Content-Disposition = %q, want filename=\"notes.json.gz\"", got)
		}
	})

	t.Run("without decompress returns gzip bytes", func(t *testing.T) {
		rec := serveFile(t, router, cfg, path, nil)
		if rec.Code != http.StatusOK {
			t.Fatalf("status = %d, want %d", rec.Code, http.StatusOK)
		}
		if rec.Body.String() == string(plain) {
			t.Fatal("body was decompressed without decompress=true")
		}
		if !bytes.Equal(rec.Body.Bytes(), gzPayload) {
			t.Fatalf("body is not the original gzip payload")
		}
	})

	t.Run("offset and length apply after gunzip", func(t *testing.T) {
		rec := serveFile(t, router, cfg, path, url.Values{
			"decompress": {"true"},
			"offset":     {"6"},
			"length":     {"4"},
		})
		if rec.Code != http.StatusOK {
			t.Fatalf("status = %d, want %d; body = %q", rec.Code, http.StatusOK, rec.Body.String())
		}
		if got := rec.Body.String(); got != "gzip" {
			t.Fatalf("body = %q, want %q", got, "gzip")
		}
		if got := rec.Header().Get("Content-Type"); got != "application/octet-stream" {
			t.Fatalf("Content-Type = %q, want application/octet-stream", got)
		}
	})
}

func TestFileEndpointGzipDecompressDeflateMember(t *testing.T) {
	plain := []byte("deflated gzip member")
	zipData := buildZipWithNamedBytes(t, "out.vasp.gz", gzipBytes(t, plain), zipDeflate)
	_, router, cfg := newZipStreamingRouter(t, zipData)

	rec := serveFile(t, router, cfg, "/file/"+fileTestUploadID+"/out.vasp.gz", url.Values{"decompress": {"1"}})
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d; body = %q", rec.Code, http.StatusOK, rec.Body.String())
	}
	if got := rec.Body.String(); got != string(plain) {
		t.Fatalf("body = %q, want %q", got, plain)
	}
}

func TestFileEndpointDecompressIgnoredForNonGzip(t *testing.T) {
	zipData := defaultFileZip(t)
	_, router, cfg := newZipStreamingRouter(t, zipData)

	rec := serveFile(t, router, cfg, "/file/"+fileTestUploadID+"/vasp/OUTCAR", url.Values{"decompress": {"true"}})
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d; body = %q", rec.Code, http.StatusOK, rec.Body.String())
	}
	if got := rec.Body.String(); got != "content-vasp/OUTCAR" {
		t.Fatalf("body = %q, want uncompressed zip member bytes", got)
	}
}

func TestFileEndpointInvalidGzip(t *testing.T) {
	zipData := buildZipWithNamedBytes(t, "broken.gz", []byte("not gzip"), zipStore)
	_, router, cfg := newZipStreamingRouter(t, zipData)

	rec := serveFile(t, router, cfg, "/file/"+fileTestUploadID+"/broken.gz", url.Values{"decompress": {"true"}})
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want %d; body = %q", rec.Code, http.StatusBadRequest, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), "failed to decompress gzip") {
		t.Fatalf("body = %q, want gzip error", rec.Body.String())
	}
}

func TestFileEndpointInvalidDecompressValue(t *testing.T) {
	zipData := defaultFileZip(t)
	_, router, cfg := newZipStreamingRouter(t, zipData)

	rec := serveFile(t, router, cfg, "/file/"+fileTestUploadID+"/vasp/OUTCAR", url.Values{"decompress": {"maybe"}})
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want %d; body = %q", rec.Code, http.StatusBadRequest, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), "invalid decompress value") {
		t.Fatalf("body = %q, want invalid decompress value", rec.Body.String())
	}
}
