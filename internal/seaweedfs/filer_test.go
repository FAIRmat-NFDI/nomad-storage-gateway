package seaweedfs

import (
	"testing"

	"github.com/FAIRmat-NFDI/nomad-storage-gateway/internal/config"
)

func TestNewFilerClient(t *testing.T) {
	client, err := NewFilerClient(config.Config{
		SeaweedFS: config.SeaweedFSConfig{FilerEndpoint: "filer.test:18888"},
	}, nil)
	if err != nil {
		t.Fatalf("NewFilerClient() error = %v", err)
	}
	if client.conn == nil {
		t.Fatal("NewFilerClient() returned a client without a connection")
	}
	if client.Client == nil {
		t.Fatal("NewFilerClient() returned a client without a filer stub")
	}

	if err := client.Close(); err != nil {
		t.Fatalf("Close() error = %v", err)
	}
}
