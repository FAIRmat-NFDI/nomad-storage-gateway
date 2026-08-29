package server

import (
	"fmt"
	"net/http"
	"path"
	"strings"

	"github.com/FAIRmat-NFDI/nomad-storage-gateway/internal/seaweedfs"
	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/go-chi/chi/v5"
	"github.com/seaweedfs/seaweedfs/weed/pb/filer_pb"
)

func fileMetadata(uploadID string, fc *seaweedfs.FilerClient) (string, error) {
	return "", nil
}

func (s *Server) zip(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	uploadID := chi.URLParam(r, "upload_id")
	if uploadID == "" || len(uploadID) <= 2 {
		http.Error(w, "missing/invalid upload id", http.StatusBadRequest)
		return
	}

	filer_req := &filer_pb.LookupDirectoryEntryRequest{
		// SeaweedFS Filer Path (for gRPC metadata check)
		Directory: fmt.Sprintf("/buckets/%s/%s/%s", s.cfg.SeaweedFS.S3Bucket, uploadID[:2], uploadID),
		// This is the name used in NOMAD for the zipped upload
		Name: "raw-public.plain.zip",
	}

	filer_resp, err := s.filerClient.Client.LookupDirectoryEntry(ctx, filer_req)
	if err != nil {
		// File not found in Filer -> return 404
		http.Error(w, "upload zip not found", http.StatusNotFound)
		return
	}

	subpath := chi.URLParam(r, "*")
	if subpath == "" {
		s.redirectUploadZip(w, r, filer_req, filer_resp)
		return
	} else {
		http.Error(w, "zipped subdirectories are not implemented", http.StatusNotImplemented)
		return
	}

}

func (s *Server) redirectUploadZip(w http.ResponseWriter, r *http.Request, filer_req *filer_pb.LookupDirectoryEntryRequest, resp *filer_pb.LookupDirectoryEntryResponse) {
	directory := filer_req.GetDirectory()
	name := filer_req.GetName()
	entry := resp.Entry
	remote := entry.GetRemoteEntry()
	var storageName, bucket string
	if remote == nil || remote.GetStorageName() == "" {
		// Case 1. File stored on seaweedfs server
		storageName = "seaweedfs"
		bucket = s.cfg.SeaweedFS.S3Bucket
	} else {
		// Case 2. File stored on a remote S3 Client
		storageName = remote.GetStorageName()
		provider, ok := s.cfg.Providers[storageName]
		if !ok {
			http.Error(w, "unknown remote storage", http.StatusBadRequest)
			return
		}
		bucket = provider.Bucket
	}

	key, err := objectKey(directory, name, s.cfg.SeaweedFS.S3Bucket)
	if err != nil {
		http.Error(w, "invalid directory key", http.StatusBadRequest)
		return
	}
	presigner, ok := s.presigners[storageName]
	if !ok {
		http.Error(w, "presigner not set", http.StatusBadRequest)
		return
	}
	presignedUrl, err := presigner.PresignGetObject(
		r.Context(),
		&s3.GetObjectInput{Bucket: aws.String(bucket), Key: aws.String(key)},
	)
	if err != nil {
		http.Error(w, fmt.Sprintf("presign failed: %v", err), http.StatusInternalServerError)
		return
	}
	http.Redirect(w, r, presignedUrl.URL, http.StatusTemporaryRedirect)
	return
}

func objectKey(directory, name, seaweedBucket string) (string, error) {
	root := "/buckets/" + seaweedBucket

	relativeDir, ok := strings.CutPrefix(directory, root)
	if !ok {
		return "", fmt.Errorf("directory %q is outside bucket %q", directory, seaweedBucket)
	}
	return path.Join(strings.TrimPrefix(relativeDir, "/"), name), nil
}
