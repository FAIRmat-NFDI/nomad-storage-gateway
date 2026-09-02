package server

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/url"
	"path"
	"strings"
	"testing"

	"github.com/FAIRmat-NFDI/nomad-storage-gateway/internal/config"
	"github.com/seaweedfs/seaweedfs/weed/pb/filer_pb"
	"google.golang.org/grpc"
)

type fakeFilerClient struct {
	request  *filer_pb.LookupDirectoryEntryRequest
	response *filer_pb.LookupDirectoryEntryResponse
	err      error
	calls    int
}

func (f *fakeFilerClient) LookupDirectoryEntry(
	_ context.Context,
	request *filer_pb.LookupDirectoryEntryRequest,
	_ ...grpc.CallOption,
) (*filer_pb.LookupDirectoryEntryResponse, error) {
	f.calls++
	f.request = request
	return f.response, f.err
}

func testConfig() config.Config {
	return config.Config{
		SeaweedFS: config.SeaweedFSConfig{
			S3Endpoint:     "http://seaweedfs.test:8333",
			S3Bucket:       "nomad-public",
			S3AccessKey:    "seaweed-access",
			S3SecretKey:    "seaweed-secret",
			FilerEndpoint:  "filer.test:18888",
			PublicEndpoint: "https://nomad-lab.eu/files",
		},
		Providers: map[string]config.ObjectStore{
			"cloud1": {
				Type:      "s3",
				Endpoint:  "http://cloud.test:9000",
				Bucket:    "nomad-published-cloud",
				AccessKey: "cloud-access",
				SecretKey: "cloud-secret",
			},
		},
	}
}

func TestZipEndpoint(t *testing.T) {
	const uploadID = "abcdef"

	tests := []struct {
		name           string
		requestPath    string
		filer          *fakeFilerClient
		wantStatus     int
		wantHost       string
		wantPathPrefix string
		wantBucket     string
		wantKey        string
		wantBodyPart   string
		wantCalls      int
	}{
		{
			name:         "filer lookup error",
			requestPath:  "/zip/" + uploadID,
			filer:        &fakeFilerClient{err: errors.New("entry not found")},
			wantStatus:   http.StatusNotFound,
			wantBodyPart: "upload zip not found",
			wantCalls:    1,
		},
		{
			name:        "local object",
			requestPath: "/zip/" + uploadID,
			filer: &fakeFilerClient{response: &filer_pb.LookupDirectoryEntryResponse{
				Entry: &filer_pb.Entry{Name: "raw-public.plain.zip"},
			}},
			wantStatus:     http.StatusTemporaryRedirect,
			wantHost:       "nomad-lab.eu",
			wantPathPrefix: "/files",
			wantBucket:     "nomad-public",
			wantKey:        "ab/abcdef/raw-public.plain.zip",
			wantCalls:      1,
		},
		{
			name:        "remote object",
			requestPath: "/zip/" + uploadID,
			filer: &fakeFilerClient{response: &filer_pb.LookupDirectoryEntryResponse{
				Entry: &filer_pb.Entry{
					Name: "raw-public.plain.zip",
					Attributes: &filer_pb.FuseAttributes{
						FileSize: 1024,
						Mtime:    100,
					},
					RemoteEntry: &filer_pb.RemoteEntry{
						StorageName:       "cloud1",
						RemoteSize:        1024,
						LastLocalSyncTsNs: 200 * 1e9,
					},
				},
			}},
			wantStatus: http.StatusTemporaryRedirect,
			wantHost:   "cloud.test:9000",
			wantBucket: "nomad-published-cloud",
			wantKey:    "ab/abcdef/raw-public.plain.zip",
			wantCalls:  1,
		},
		{
			name:        "stale remote object falls back to local seaweedfs",
			requestPath: "/zip/" + uploadID,
			filer: &fakeFilerClient{response: &filer_pb.LookupDirectoryEntryResponse{
				Entry: &filer_pb.Entry{
					Name:   "raw-public.plain.zip",
					Chunks: []*filer_pb.FileChunk{{FileId: "local_chunk"}},
					Attributes: &filer_pb.FuseAttributes{
						FileSize: 1024,
						Mtime:    300, // modified after sync
					},
					RemoteEntry: &filer_pb.RemoteEntry{
						StorageName:       "cloud1",
						RemoteSize:        1024,
						LastLocalSyncTsNs: 200 * 1e9,
					},
				},
			}},
			wantStatus:     http.StatusTemporaryRedirect,
			wantHost:       "nomad-lab.eu",
			wantPathPrefix: "/files",
			wantBucket:     "nomad-public",
			wantKey:        "ab/abcdef/raw-public.plain.zip",
			wantCalls:      1,
		},
		{
			name:        "unknown remote object falls back to local seaweedfs",
			requestPath: "/zip/" + uploadID,
			filer: &fakeFilerClient{response: &filer_pb.LookupDirectoryEntryResponse{
				Entry: &filer_pb.Entry{
					Name: "raw-public.plain.zip",
					Attributes: &filer_pb.FuseAttributes{
						FileSize: 1024,
						Mtime:    100,
					},
					RemoteEntry: &filer_pb.RemoteEntry{
						StorageName:       "missing",
						RemoteSize:        1024,
						LastLocalSyncTsNs: 200 * 1e9,
					},
				},
			}},
			wantStatus:     http.StatusTemporaryRedirect,
			wantHost:       "nomad-lab.eu",
			wantPathPrefix: "/files",
			wantBucket:     "nomad-public",
			wantKey:        "ab/abcdef/raw-public.plain.zip",
			wantCalls:      1,
		},
		{
			name:        "zip subpath",
			requestPath: "/zip/" + uploadID + "/input/INCAR",
			filer: &fakeFilerClient{response: &filer_pb.LookupDirectoryEntryResponse{
				Entry: &filer_pb.Entry{Name: "raw-public.plain.zip"},
			}},
			wantStatus:   http.StatusNotImplemented,
			wantBodyPart: "zipped subdirectories are not implemented",
			wantCalls:    1,
		},
		{
			name:         "malformed filer response",
			requestPath:  "/zip/" + uploadID,
			filer:        &fakeFilerClient{response: &filer_pb.LookupDirectoryEntryResponse{}},
			wantStatus:   http.StatusBadGateway,
			wantBodyPart: "invalid filer response",
			wantCalls:    1,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			router, err := NewRouter(testConfig(), tt.filer)
			if err != nil {
				t.Fatalf("NewRouter() error = %v", err)
			}

			req := httptest.NewRequest(http.MethodGet, tt.requestPath, nil)
			rec := httptest.NewRecorder()
			router.ServeHTTP(rec, req)

			if rec.Code != tt.wantStatus {
				t.Fatalf("status = %d, want %d; body = %q", rec.Code, tt.wantStatus, rec.Body.String())
			}
			if tt.wantBodyPart != "" && !strings.Contains(rec.Body.String(), tt.wantBodyPart) {
				t.Fatalf("body = %q, want it to contain %q", rec.Body.String(), tt.wantBodyPart)
			}
			if tt.filer.calls != tt.wantCalls {
				t.Fatalf("LookupDirectoryEntry calls = %d, want %d", tt.filer.calls, tt.wantCalls)
			}
			if tt.filer.request != nil {
				if got, want := tt.filer.request.GetDirectory(), "/buckets/nomad-public/ab/"+uploadID; got != want {
					t.Errorf("lookup directory = %q, want %q", got, want)
				}
				if got, want := tt.filer.request.GetName(), "raw-public.plain.zip"; got != want {
					t.Errorf("lookup name = %q, want %q", got, want)
				}
			}
			if tt.wantHost != "" {
				assertRedirect(t, rec, tt.wantHost, tt.wantPathPrefix, tt.wantBucket, tt.wantKey)
			}
		})
	}
}

func TestZipEndpointRejectsInvalidUploadID(t *testing.T) {
	filer := &fakeFilerClient{}
	router, err := NewRouter(testConfig(), filer)
	if err != nil {
		t.Fatalf("NewRouter() error = %v", err)
	}

	req := httptest.NewRequest(http.MethodGet, "/zip/ab", nil)
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusBadRequest)
	}
	if filer.calls != 0 {
		t.Fatalf("LookupDirectoryEntry calls = %d, want 0", filer.calls)
	}
}

func TestZipEndpointRequiresFilerClient(t *testing.T) {
	router, err := NewRouter(testConfig(), nil)
	if err != nil {
		t.Fatalf("NewRouter() error = %v", err)
	}

	req := httptest.NewRequest(http.MethodGet, "/zip/abcdef", nil)
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)

	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusInternalServerError)
	}
}

func TestObjectKey(t *testing.T) {
	tests := []struct {
		name          string
		directory     string
		fileName      string
		bucket        string
		wantKey       string
		wantErrorPart string
	}{
		{
			name:      "nested directory",
			directory: "/buckets/nomad-public/ab/abcdef",
			fileName:  "raw-public.plain.zip",
			bucket:    "nomad-public",
			wantKey:   "ab/abcdef/raw-public.plain.zip",
		},
		{
			name:      "bucket root",
			directory: "/buckets/nomad-public",
			fileName:  "object",
			bucket:    "nomad-public",
			wantKey:   "object",
		},
		{
			name:          "bucket name prefix is not enough",
			directory:     "/buckets/nomad-public-other/ab/abcdef",
			fileName:      "object",
			bucket:        "nomad-public",
			wantErrorPart: "outside bucket",
		},
		{
			name:          "different root",
			directory:     "/other/ab/abcdef",
			fileName:      "object",
			bucket:        "nomad-public",
			wantErrorPart: "outside bucket",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := objectKey(tt.directory, tt.fileName, tt.bucket)
			if tt.wantErrorPart != "" {
				if err == nil {
					t.Fatal("objectKey() error = nil, want error")
				}
				if !strings.Contains(err.Error(), tt.wantErrorPart) {
					t.Errorf("error = %q, want it to contain %q", err, tt.wantErrorPart)
				}
				return
			}
			if err != nil {
				t.Fatalf("objectKey() error = %v", err)
			}
			if got != tt.wantKey {
				t.Errorf("objectKey() = %q, want %q", got, tt.wantKey)
			}
		})
	}
}

func assertRedirect(t *testing.T, rec *httptest.ResponseRecorder, wantHost, wantPathPrefix, wantBucket, wantKey string) {
	t.Helper()

	location := rec.Header().Get("Location")
	u, err := url.Parse(location)
	if err != nil {
		t.Fatalf("parse redirect location %q: %v", location, err)
	}
	if u.Host != wantHost {
		t.Errorf("redirect host = %q, want %q", u.Host, wantHost)
	}
	if got, want := u.Path, path.Join("/", wantPathPrefix, wantBucket, wantKey); got != want {
		t.Errorf("redirect path = %q, want %q", got, want)
	}
	if got := u.Query().Get("X-Amz-Signature"); got == "" {
		t.Error("redirect is missing an AWS signature")
	}
}

func TestIsCloudFresh(t *testing.T) {
	const (
		syncTimeNs  = 1_700_000_000_000_000_000
		syncTimeSec = syncTimeNs / 1e9
		fileSize    = uint64(2048)
	)

	providers := map[string]config.ObjectStore{
		"cloud1": {Bucket: "cloud-bucket"},
	}

	tests := []struct {
		name      string
		entry     *filer_pb.Entry
		wantFresh bool
	}{
		{
			name:      "nil entry",
			entry:     nil,
			wantFresh: false,
		},
		{
			name: "local only (no remote entry)",
			entry: &filer_pb.Entry{
				Attributes: &filer_pb.FuseAttributes{FileSize: fileSize, Mtime: syncTimeSec},
			},
			wantFresh: false,
		},
		{
			name: "no attributes",
			entry: &filer_pb.Entry{
				RemoteEntry: &filer_pb.RemoteEntry{
					StorageName:       "cloud1",
					RemoteSize:        int64(fileSize),
					LastLocalSyncTsNs: syncTimeNs,
				},
			},
			wantFresh: false,
		},
		{
			name: "remote-only object (mounted or uncached, no local chunks, LastLocalSyncTsNs == 0)",
			entry: &filer_pb.Entry{
				Attributes: &filer_pb.FuseAttributes{FileSize: fileSize, Mtime: syncTimeSec},
				RemoteEntry: &filer_pb.RemoteEntry{
					StorageName:       "cloud1",
					RemoteSize:        int64(fileSize),
					LastLocalSyncTsNs: 0,
				},
			},
			wantFresh: true,
		},
		{
			name: "locally cached entry exists but never synced (LastLocalSyncTsNs == 0)",
			entry: &filer_pb.Entry{
				Chunks:     []*filer_pb.FileChunk{{FileId: "chunk1"}},
				Attributes: &filer_pb.FuseAttributes{FileSize: fileSize, Mtime: syncTimeSec},
				RemoteEntry: &filer_pb.RemoteEntry{
					StorageName:       "cloud1",
					RemoteSize:        int64(fileSize),
					LastLocalSyncTsNs: 0,
				},
			},
			wantFresh: false,
		},
		{
			name: "stale cloud: locally cached and local mtime is newer than sync time",
			entry: &filer_pb.Entry{
				Chunks: []*filer_pb.FileChunk{{FileId: "chunk1"}},
				Attributes: &filer_pb.FuseAttributes{
					FileSize: fileSize,
					Mtime:    syncTimeSec + 10,
				},
				RemoteEntry: &filer_pb.RemoteEntry{
					StorageName:       "cloud1",
					RemoteSize:        int64(fileSize),
					LastLocalSyncTsNs: syncTimeNs,
				},
			},
			wantFresh: false,
		},
		{
			name: "size mismatch",
			entry: &filer_pb.Entry{
				Attributes: &filer_pb.FuseAttributes{
					FileSize: fileSize + 500,
					Mtime:    syncTimeSec - 10,
				},
				RemoteEntry: &filer_pb.RemoteEntry{
					StorageName:       "cloud1",
					RemoteSize:        int64(fileSize),
					LastLocalSyncTsNs: syncTimeNs,
				},
			},
			wantFresh: false,
		},
		{
			name: "unknown remote provider",
			entry: &filer_pb.Entry{
				Attributes: &filer_pb.FuseAttributes{FileSize: fileSize, Mtime: syncTimeSec - 10},
				RemoteEntry: &filer_pb.RemoteEntry{
					StorageName:       "unknown_provider",
					RemoteSize:        int64(fileSize),
					LastLocalSyncTsNs: syncTimeNs,
				},
			},
			wantFresh: false,
		},
		{
			name: "fresh: mtime older than sync time, size matches, known provider",
			entry: &filer_pb.Entry{
				Chunks: []*filer_pb.FileChunk{{FileId: "chunk1"}},
				Attributes: &filer_pb.FuseAttributes{
					FileSize: fileSize,
					Mtime:    syncTimeSec - 60,
				},
				RemoteEntry: &filer_pb.RemoteEntry{
					StorageName:       "cloud1",
					RemoteSize:        int64(fileSize),
					LastLocalSyncTsNs: syncTimeNs,
				},
			},
			wantFresh: true,
		},
		{
			name: "fresh: exact boundary where mtime equals sync time",
			entry: &filer_pb.Entry{
				Attributes: &filer_pb.FuseAttributes{
					FileSize: fileSize,
					Mtime:    syncTimeSec,
				},
				RemoteEntry: &filer_pb.RemoteEntry{
					StorageName:       "cloud1",
					RemoteSize:        int64(fileSize),
					LastLocalSyncTsNs: syncTimeNs,
				},
			},
			wantFresh: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := isCloudFresh(tt.entry, providers)
			if got != tt.wantFresh {
				t.Errorf("isCloudFresh() = %v, want %v", got, tt.wantFresh)
			}
		})
	}
}
