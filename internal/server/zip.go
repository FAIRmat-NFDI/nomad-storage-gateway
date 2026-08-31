package server

import (
	"context"
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
	if uploadID == "" || len(uploadID) <= 2 {
		http.Error(w, "missing/invalid upload id", http.StatusBadRequest)
		return
	}

	filerReq := &filer_pb.LookupDirectoryEntryRequest{
		// SeaweedFS Filer Path (for gRPC metadata check)
		Directory: fmt.Sprintf("/buckets/%s/%s/%s", s.cfg.SeaweedFS.S3Bucket, uploadID[:2], uploadID),
		// This is the name used in NOMAD for the zipped upload
		Name: "raw-public.plain.zip",
	}

	if s.filerClient == nil {
		http.Error(w, "filer client is not configured", http.StatusInternalServerError)
		return
	}

	filerResp, err := s.filerClient.LookupDirectoryEntry(ctx, filerReq)
	if err != nil {
		// File not found in Filer -> return 404
		http.Error(w, "upload zip not found", http.StatusNotFound)
		return
	}

	subpath := chi.URLParam(r, "*")
	if subpath != "" {
		http.Error(w, "zipped subdirectories are not implemented", http.StatusNotImplemented)
		return
	}

	s.redirectUploadZip(w, r, filerReq, filerResp)
}

func (s *Server) redirectUploadZip(w http.ResponseWriter, r *http.Request, filerReq *filer_pb.LookupDirectoryEntryRequest, resp *filer_pb.LookupDirectoryEntryResponse) {
	directory := filerReq.GetDirectory()
	name := filerReq.GetName()
	if resp == nil || resp.Entry == nil {
		http.Error(w, "invalid filer response", http.StatusBadGateway)
		return
	}
	entry := resp.Entry
	remote := entry.GetRemoteEntry()
	var storageName, bucket string
	if remote == nil || remote.GetStorageName() == "" {
		// Case 1. File stored on seaweedfs server
		storageName = centralSeaweedFSProvider
		bucket = s.cfg.SeaweedFS.S3Bucket
	} else {
		// Case 2. File stored on a remote S3 Client
		storageName = remote.GetStorageName()
		provider, ok := s.cfg.Providers[storageName]
		if !ok {
			http.Error(w, "unknown remote storage", http.StatusBadGateway)
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
}

func objectKey(directory, name, seaweedBucket string) (string, error) {
	root := "/buckets/" + seaweedBucket

	if directory != root && !strings.HasPrefix(directory, root+"/") {
		return "", fmt.Errorf("directory %q is outside bucket %q", directory, seaweedBucket)
	}
	relativeDir := strings.TrimPrefix(directory, root)
	return path.Join(strings.TrimPrefix(relativeDir, "/"), name), nil
}
