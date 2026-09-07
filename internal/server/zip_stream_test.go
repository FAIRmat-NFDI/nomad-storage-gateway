package server

import (
	"archive/zip"
	"bytes"
	"compress/flate"
	"io"
	"strings"
	"testing"
)

func buildTestZip(t *testing.T, entries map[string]zipMethod) []byte {
	t.Helper()

	var buf bytes.Buffer
	w := zip.NewWriter(&buf)
	w.RegisterCompressor(zip.Deflate, func(out io.Writer) (io.WriteCloser, error) {
		return flate.NewWriter(out, flate.BestCompression)
	})

	for name, method := range entries {
		var fw io.Writer
		var err error
		if method == zipDeflate {
			header := &zip.FileHeader{
				Name:   name,
				Method: zip.Deflate,
			}
			fw, err = w.CreateHeader(header)
		} else {
			fw, err = w.CreateHeader(&zip.FileHeader{
				Name:   name,
				Method: zip.Store,
			})
		}
		if err != nil {
			t.Fatalf("create zip entry %q: %v", name, err)
		}
		if _, err := fw.Write([]byte("content-" + name)); err != nil {
			t.Fatalf("write zip entry %q: %v", name, err)
		}
	}
	if err := w.Close(); err != nil {
		t.Fatalf("close zip: %v", err)
	}
	return buf.Bytes()
}

type zipMethod int

const (
	zipDeflate zipMethod = iota
	zipStore
)

func openTestZip(t *testing.T, data []byte) []*zip.File {
	t.Helper()

	reader, err := zip.NewReader(bytes.NewReader(data), int64(len(data)))
	if err != nil {
		t.Fatalf("open zip: %v", err)
	}
	return reader.File
}

func readZipFileContent(t *testing.T, file *zip.File) string {
	t.Helper()

	rc, err := file.Open()
	if err != nil {
		t.Fatalf("open zip entry %q: %v", file.Name, err)
	}
	defer rc.Close()

	data, err := io.ReadAll(rc)
	if err != nil {
		t.Fatalf("read zip entry %q: %v", file.Name, err)
	}
	return string(data)
}

func zipContentsByName(t *testing.T, files []*zip.File) map[string]string {
	t.Helper()

	contents := make(map[string]string, len(files))
	for _, file := range files {
		contents[file.Name] = readZipFileContent(t, file)
	}
	return contents
}

func TestMatchZipFiles(t *testing.T) {
	data := buildTestZip(t, map[string]zipMethod{
		"vasp/OUTCAR": zipDeflate,
		"vasp/INCAR":  zipStore,
		"other/file":  zipDeflate,
	})
	files := openTestZip(t, data)

	tests := []struct {
		name       string
		subpath    string
		wantNames  []string
		singleFile bool
	}{
		{
			name:      "directory prefix",
			subpath:   "vasp/",
			wantNames: []string{"vasp/OUTCAR", "vasp/INCAR"},
		},
		{
			name:       "single file",
			subpath:    "vasp/OUTCAR",
			wantNames:  []string{"vasp/OUTCAR"},
			singleFile: true,
		},
		{
			name:      "missing path",
			subpath:   "missing/path",
			wantNames: nil,
		},
		{
			name:      "directory with trailing slash",
			subpath:   "vasp/",
			wantNames: []string{"vasp/OUTCAR", "vasp/INCAR"},
		},
		{
			name:      "directory without trailing slash",
			subpath:   "vasp",
			wantNames: []string{"vasp/OUTCAR", "vasp/INCAR"},
		},
		{
			name:       "backslash subpath",
			subpath:    `vasp\OUTCAR`,
			wantNames:  []string{"vasp/OUTCAR"},
			singleFile: true,
		},
		{
			name:      "trailing backslash directory",
			subpath:   `vasp\`,
			wantNames: []string{"vasp/OUTCAR", "vasp/INCAR"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			matched, singleFile := matchZipFiles(files, tt.subpath)
			if singleFile != tt.singleFile {
				t.Fatalf("singleFile = %v, want %v", singleFile, tt.singleFile)
			}
			if len(matched) != len(tt.wantNames) {
				t.Fatalf("matched count = %d, want %d", len(matched), len(tt.wantNames))
			}
			for i, file := range matched {
				got := strings.ReplaceAll(file.Name, "\\", "/")
				if got != tt.wantNames[i] {
					t.Errorf("matched[%d] = %q, want %q", i, got, tt.wantNames[i])
				}
			}
		})
	}
}

func TestWriteZipSubset(t *testing.T) {
	data := buildTestZip(t, map[string]zipMethod{
		"vasp/OUTCAR": zipDeflate,
		"vasp/INCAR":  zipStore,
	})
	files := openTestZip(t, data)

	t.Run("directory rewrites relative names", func(t *testing.T) {
		var buf bytes.Buffer
		matched, singleFile := matchZipFiles(files, "vasp/")
		if err := writeZipSubset(&buf, matched, "vasp/", singleFile); err != nil {
			t.Fatalf("writeZipSubset() error = %v", err)
		}
		out, err := zip.NewReader(bytes.NewReader(buf.Bytes()), int64(buf.Len()))
		if err != nil {
			t.Fatalf("open output zip: %v", err)
		}
		if len(out.File) != 2 {
			t.Fatalf("output entries = %d, want 2", len(out.File))
		}
		names := map[string]bool{}
		for _, f := range out.File {
			names[f.Name] = true
		}
		if !names["OUTCAR"] || !names["INCAR"] {
			t.Fatalf("output names = %v, want OUTCAR and INCAR", names)
		}
		for _, f := range out.File {
			if strings.HasPrefix(f.Name, "vasp/") {
				t.Fatalf("output name %q still has vasp/ prefix", f.Name)
			}
		}
		contents := zipContentsByName(t, out.File)
		if contents["OUTCAR"] != "content-vasp/OUTCAR" {
			t.Errorf("OUTCAR content = %q, want %q", contents["OUTCAR"], "content-vasp/OUTCAR")
		}
		if contents["INCAR"] != "content-vasp/INCAR" {
			t.Errorf("INCAR content = %q, want %q", contents["INCAR"], "content-vasp/INCAR")
		}
	})

	t.Run("single file uses basename", func(t *testing.T) {
		var buf bytes.Buffer
		matched, singleFile := matchZipFiles(files, "vasp/OUTCAR")
		if err := writeZipSubset(&buf, matched, "vasp/OUTCAR", singleFile); err != nil {
			t.Fatalf("writeZipSubset() error = %v", err)
		}
		out, err := zip.NewReader(bytes.NewReader(buf.Bytes()), int64(buf.Len()))
		if err != nil {
			t.Fatalf("open output zip: %v", err)
		}
		if len(out.File) != 1 {
			t.Fatalf("output entries = %d, want 1", len(out.File))
		}
		if out.File[0].Name != "OUTCAR" {
			t.Errorf("output name = %q, want OUTCAR", out.File[0].Name)
		}
		if got := readZipFileContent(t, out.File[0]); got != "content-vasp/OUTCAR" {
			t.Errorf("OUTCAR content = %q, want %q", got, "content-vasp/OUTCAR")
		}
	})

	t.Run("deflate members stay deflate", func(t *testing.T) {
		var buf bytes.Buffer
		matched, singleFile := matchZipFiles(files, "vasp/OUTCAR")
		if err := writeZipSubset(&buf, matched, "vasp/OUTCAR", singleFile); err != nil {
			t.Fatalf("writeZipSubset() error = %v", err)
		}
		out, err := zip.NewReader(bytes.NewReader(buf.Bytes()), int64(buf.Len()))
		if err != nil {
			t.Fatalf("open output zip: %v", err)
		}
		if out.File[0].Method != zip.Deflate {
			t.Errorf("method = %d, want Deflate", out.File[0].Method)
		}
		if got := readZipFileContent(t, out.File[0]); got != "content-vasp/OUTCAR" {
			t.Errorf("OUTCAR content = %q, want %q", got, "content-vasp/OUTCAR")
		}
	})

	t.Run("store members recompressed to deflate", func(t *testing.T) {
		var buf bytes.Buffer
		matched, singleFile := matchZipFiles(files, "vasp/INCAR")
		if err := writeZipSubset(&buf, matched, "vasp/INCAR", singleFile); err != nil {
			t.Fatalf("writeZipSubset() error = %v", err)
		}
		out, err := zip.NewReader(bytes.NewReader(buf.Bytes()), int64(buf.Len()))
		if err != nil {
			t.Fatalf("open output zip: %v", err)
		}
		if out.File[0].Method != zip.Deflate {
			t.Errorf("method = %d, want Deflate", out.File[0].Method)
		}
		if got := readZipFileContent(t, out.File[0]); got != "content-vasp/INCAR" {
			t.Errorf("INCAR content = %q, want %q", got, "content-vasp/INCAR")
		}
	})
}
