package seaweedfs

import (
	"context"
	"testing"
	"time"

	"github.com/FAIRmat-NFDI/nomad-storage-gateway/internal/config"
	"github.com/seaweedfs/seaweedfs/weed/pb/filer_pb"
)

func TestLookupEntry(t *testing.T) {
	cfg := config.Config{
		SeaweedFS: config.SeaweedFSConfig{
			FilerEndpoint: "localhost:18888",
		},
	}
	fc, err := NewFilerClient(cfg, nil)
	if err != nil {
		t.Fatalf("failed to connect: %v", err)
	}
	defer fc.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()

	// 1. Check Local Upload (no remote entry)
	localResp, err := fc.Client.LookupDirectoryEntry(ctx, &filer_pb.LookupDirectoryEntryRequest{
		Directory: "/buckets/nomad-public/no/nomad_local_upload",
		Name:      "raw-public.plain.zip",
	})
	if err != nil {
		t.Fatalf("Lookup local failed: %v", err)
	}
	t.Logf("Local Entry: Name=%s, Size=%d bytes, Chunks=%d, Remote=%v",
		localResp.Entry.Name,
		localResp.Entry.Attributes.FileSize,
		len(localResp.Entry.Chunks),
		localResp.Entry.RemoteEntry,
	)

	// 2. Check Cloud Tiered Upload (has remote entry)
	cloudResp, err := fc.Client.LookupDirectoryEntry(ctx, &filer_pb.LookupDirectoryEntryRequest{
		Directory: "/buckets/nomad-public/no/nomad_cloud_upload",
		Name:      "raw-public.plain.zip",
	})
	if err != nil {
		t.Fatalf("Lookup cloud failed: %v", err)
	}
	t.Logf("Cloud Entry: Name=%s, Size=%d bytes, StorageName=%s, ETag=%s",
		cloudResp.Entry.Name,
		cloudResp.Entry.Attributes.FileSize,
		cloudResp.Entry.RemoteEntry.StorageName,
		cloudResp.Entry.RemoteEntry.RemoteETag,
	)
}
