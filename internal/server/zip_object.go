package server

import (
	"archive/zip"
	"context"
	"fmt"
	"net/http"
	"path"
	"strings"

	"github.com/FAIRmat-NFDI/nomad-storage-gateway/internal/config"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/seaweedfs/seaweedfs/weed/pb/filer_pb"
)

type zipObject struct {
	client *s3.Client
	bucket string
	key    string
	size   int64
}

type zipResolveError struct {
	status  int
	message string
}

func (e *zipResolveError) Error() string {
	return e.message
}

func (s *Server) resolveZipObject(ctx context.Context, uploadID string) (*zipObject, error) {
	prefixSize := s.cfg.SeaweedFS.PrefixSize
	if prefixSize <= 0 {
		prefixSize = 2
	}
	if uploadID == "" || len(uploadID) <= prefixSize {
		return nil, &zipResolveError{status: http.StatusBadRequest, message: "missing/invalid upload id"}
	}

	directory := fmt.Sprintf(
		"/buckets/%s/%s/%s",
		s.cfg.SeaweedFS.S3Bucket,
		uploadID[:prefixSize],
		uploadID,
	)
	filerReq := &filer_pb.LookupDirectoryEntryRequest{
		// SeaweedFS Filer Path (for gRPC metadata check)
		Directory: directory,
		// This is the name used in NOMAD for the zipped upload
		Name: "raw-public.plain.zip",
	}

	if s.filerClient == nil {
		return nil, &zipResolveError{status: http.StatusInternalServerError, message: "filer client is not configured"}
	}

	filerResp, err := s.filerClient.LookupDirectoryEntry(ctx, filerReq)
	if err != nil {
		// File not found in Filer -> return 404
		return nil, &zipResolveError{status: http.StatusNotFound, message: "upload zip not found"}
	}
	entry := filerResp.GetEntry()
	if entry == nil {
		return nil, &zipResolveError{status: http.StatusBadGateway, message: "invalid filer response"}
	}

	var storageName, bucket string
	if isCloudFresh(entry, s.cfg.Providers) {
		// Case 1. File stored on a remote S3 Client
		remote := entry.GetRemoteEntry()
		storageName = remote.GetStorageName()
		provider, ok := s.cfg.Providers[storageName]
		if !ok {
			return nil, &zipResolveError{status: http.StatusBadGateway, message: "unknown remote storage"}
		}
		bucket = provider.Bucket
	} else {
		// Case 2. File stored on seaweedfs server
		storageName = centralSeaweedFSProvider
		bucket = s.cfg.SeaweedFS.S3Bucket
	}

	name := filerReq.GetName()
	key, err := objectKey(directory, name, s.cfg.SeaweedFS.S3Bucket)
	if err != nil {
		return nil, &zipResolveError{status: http.StatusBadRequest, message: "invalid directory key"}
	}

	client, ok := s.clients[storageName]
	if !ok {
		return nil, &zipResolveError{status: http.StatusBadGateway, message: "client not set"}
	}

	attrs := entry.GetAttributes()
	var size int64
	if attrs != nil {
		size = int64(attrs.GetFileSize())
	}

	return &zipObject{
		client: client,
		bucket: bucket,
		key:    key,
		size:   size,
	}, nil
}

func (s *Server) getOrLoadZipReader(obj *zipObject) (*zip.Reader, error) {
	cacheKey := obj.bucket + "/" + obj.key
	if cached, ok := s.zipCache.Load(cacheKey); ok {
		return cached.(*zip.Reader), nil
	}
	// Merge concurrent requests for the same uncached upload:
	res, err, _ := s.sfGroup.Do(cacheKey, func() (any, error) {
		if cached, ok := s.zipCache.Load(cacheKey); ok {
			return cached, nil
		}
		// Use background context so the reader survives beyond any single HTTP request
		reader := newZipObjectReader(context.Background(), obj.client, obj.bucket, obj.key)
		zr, err := zip.NewReader(reader, obj.size)
		if err != nil {
			return nil, err
		}
		s.zipCache.Store(cacheKey, zr)
		return zr, nil

	})
	if err != nil {
		return nil, err
	}
	return res.(*zip.Reader), nil
}

func isCloudFresh(entry *filer_pb.Entry, configuredProviders map[string]config.ObjectStore) bool {
	if entry == nil {
		return false
	}
	remote := entry.GetRemoteEntry()
	attrs := entry.GetAttributes()
	if remote == nil || attrs == nil {
		return false
	}

	// 1. Remote provider must be registered in gateway config (e.g. "cloud1")
	if _, ok := configuredProviders[remote.GetStorageName()]; !ok {
		return false
	}

	// 2. Current logical size must match the remote size
	if remote.GetRemoteSize() < 0 || attrs.GetFileSize() != uint64(remote.GetRemoteSize()) {
		return false
	}

	// 3. If entry is remote-only (no local chunks, e.g. mounted or uncached via weed shell),
	// the data authoritatively lives in the remote cloud provider.
	if entry.IsInRemoteOnly() {
		return true
	}

	// 4. For locally cached entries (with local chunks), check synchronization freshness.
	// Must have been synced to the remote provider, and local mtime must NOT be newer than the sync timestamp.
	if remote.GetLastLocalSyncTsNs() <= 0 {
		return false
	}

	localMtimeNs := attrs.GetMtime()*1_000_000_000 + int64(attrs.GetMtimeNs())
	if localMtimeNs > remote.GetLastLocalSyncTsNs() {
		return false
	}

	return true
}

func objectKey(directory, name, seaweedBucket string) (string, error) {
	root := "/buckets/" + seaweedBucket

	if directory != root && !strings.HasPrefix(directory, root+"/") {
		return "", fmt.Errorf("directory %q is outside bucket %q", directory, seaweedBucket)
	}
	relativeDir := strings.TrimPrefix(directory, root)
	return path.Join(strings.TrimPrefix(relativeDir, "/"), name), nil
}
