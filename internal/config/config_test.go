package internal

import (
	"os"
	"path/filepath"
	"testing"
)

func TestLoad(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.yaml")

	configYAML := `
port: 8080
seaweedfs:
  s3_endpoint: http://seaweedfs:8333
  s3_bucket: nomad-public
  s3_access_key: access_key
  s3_secret_key: secret_key
`

	if err := os.WriteFile(path, []byte(configYAML), 0600); err != nil {
		t.Fatal(err)
	}

	cfg, err := Load(path)
	if err != nil {
		t.Fatalf("Load() failed: %v", err)
	}

	if cfg.Port != 8080 {
		t.Errorf("expected port 8080, got %d", cfg.Port)
	}

	if cfg.SeaweedFS.S3Endpoint != "http://seaweedfs:8333" {
		t.Errorf("unexpected SeaweedFS endpoint: %q", cfg.SeaweedFS.S3Endpoint)
	}
}

func TestLoadMissingFile(t *testing.T) {
	_, err := Load("/does/not/exist/config.yaml")
	if err == nil {
		t.Fatal("expected error for missing config file")
	}
}
