//go:build e2e

package e2e

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/FAIRmat-NFDI/nomad-storage-gateway/internal/config"
	"github.com/FAIRmat-NFDI/nomad-storage-gateway/internal/seaweedfs"
	"github.com/aws/aws-sdk-go-v2/aws"
	awsconfig "github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/credentials"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/aws/smithy-go"
	"github.com/seaweedfs/seaweedfs/weed/pb/filer_pb"
)

const (
	localAccessKey    = "nomad_access_key"
	localSecretKey    = "nomad_secret_key"
	cloudAccessKey    = "cloudadmin"
	cloudSecretKey    = "cloudpassword123"
	localBucket       = "nomad-e2e"
	cloudBucket       = "nomad-e2e-cloud"
	zipFileName       = "raw-public.plain.zip"
	serviceWaitPeriod = 90 * time.Second
)

func TestDownloadZipWithComposeServices(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), serviceWaitPeriod)
	defer cancel()

	localEndpoint := envOrDefault("E2E_SEAWEEDFS_S3_ENDPOINT", "http://127.0.0.1:8333")
	publicEndpoint := envOrDefault("E2E_SEAWEEDFS_PUBLIC_ENDPOINT", localEndpoint)
	cloudEndpoint := envOrDefault("E2E_RUSTFS_ENDPOINT", "http://127.0.0.1:9000")
	filerEndpoint := envOrDefault("E2E_SEAWEEDFS_FILER_ENDPOINT", "127.0.0.1:18888")

	localS3, err := newS3Client(ctx, localEndpoint, localAccessKey, localSecretKey)
	if err != nil {
		t.Fatalf("create SeaweedFS S3 client: %v", err)
	}
	cloudS3, err := newS3Client(ctx, cloudEndpoint, cloudAccessKey, cloudSecretKey)
	if err != nil {
		t.Fatalf("create RustFS S3 client: %v", err)
	}

	if err := waitFor(ctx, func(ctx context.Context) error {
		return ensureBucket(ctx, localS3, localBucket)
	}); err != nil {
		t.Fatalf("wait for SeaweedFS S3: %v", err)
	}
	if err := waitFor(ctx, func(ctx context.Context) error {
		return ensureBucket(ctx, cloudS3, cloudBucket)
	}); err != nil {
		t.Fatalf("wait for RustFS S3: %v", err)
	}

	filer, err := seaweedfs.NewFilerClient(config.Config{
		SeaweedFS: config.SeaweedFSConfig{FilerEndpoint: filerEndpoint},
	}, nil)
	if err != nil {
		t.Fatalf("create SeaweedFS filer client: %v", err)
	}
	defer filer.Close()

	if err := waitFor(ctx, func(ctx context.Context) error {
		_, err := filer.Client.Ping(ctx, &filer_pb.PingRequest{})
		return err
	}); err != nil {
		t.Fatalf("wait for SeaweedFS filer: %v", err)
	}

	localUploadID := fmt.Sprintf("e2e-local-%d", time.Now().UnixNano())
	remoteUploadID := fmt.Sprintf("e2e-remote-%d", time.Now().UnixNano())
	localBody := []byte("zip content served by SeaweedFS")
	remoteBody := []byte("zip content served by RustFS")

	if err := putObject(ctx, localS3, localBucket, uploadKey(localUploadID), localBody); err != nil {
		t.Fatalf("seed local zip: %v", err)
	}
	if err := putObject(ctx, cloudS3, cloudBucket, uploadKey(remoteUploadID), remoteBody); err != nil {
		t.Fatalf("seed remote zip: %v", err)
	}

	if err := mountRemoteZip(ctx, remoteUploadID); err != nil {
		t.Fatalf("mount remote zip through SeaweedFS: %v", err)
	}

	if _, err := waitForEntry(ctx, filer.Client, uploadDirectory(localBucket, localUploadID), zipFileName); err != nil {
		t.Fatalf("wait for local filer metadata: %v", err)
	}
	if entry, err := waitForEntry(ctx, filer.Client, uploadDirectory(localBucket, remoteUploadID), zipFileName); err != nil {
		t.Fatalf("wait for remote filer metadata: %v", err)
	} else if entry.GetRemoteEntry().GetStorageName() != "cloud1" {
		t.Fatalf("remote storage name = %q, want %q", entry.GetRemoteEntry().GetStorageName(), "cloud1")
	} else if got := entry.GetRemoteEntry().GetRemoteSize(); got != int64(len(remoteBody)) {
		t.Fatalf("remote object size = %d, want %d", got, len(remoteBody))
	}

	gatewayURL := envOrDefault("E2E_GATEWAY_URL", "http://127.0.0.1:3333")
	gatewayPort := envOrDefault("E2E_GATEWAY_PORT", "3333")
	gatewayBinary := buildGateway(t, ctx)
	startGateway(t, ctx, gatewayBinary, gatewayPort, localEndpoint, publicEndpoint, filerEndpoint, cloudEndpoint)
	if err := waitForGateway(ctx, gatewayURL); err != nil {
		t.Fatalf("wait for gateway: %v", err)
	}

	t.Run("local SeaweedFS object", func(t *testing.T) {
		assertDownload(t, ctx, gatewayURL, localUploadID, publicEndpoint, localBucket, localBody)
	})
	t.Run("remote RustFS object", func(t *testing.T) {
		assertDownload(t, ctx, gatewayURL, remoteUploadID, cloudEndpoint, cloudBucket, remoteBody)
	})
}

func buildGateway(t *testing.T, ctx context.Context) string {
	t.Helper()

	binaryPath := filepath.Join(t.TempDir(), "gateway")
	packageRoot := repositoryRoot()
	command := exec.CommandContext(ctx, "go", "build", "-trimpath", "-o", binaryPath, "./cmd/gateway")
	command.Dir = packageRoot
	output, err := command.CombinedOutput()
	if err != nil {
		t.Fatalf("build gateway: %v\n%s", err, output)
	}
	return binaryPath
}

func startGateway(t *testing.T, ctx context.Context, binaryPath, port, localEndpoint, publicEndpoint, filerEndpoint, cloudEndpoint string) {
	t.Helper()

	processCtx, cancel := context.WithCancel(ctx)
	command := exec.CommandContext(processCtx, binaryPath)
	command.Dir = repositoryRoot()
	command.Env = append(os.Environ(),
		"NOMAD_PORT="+port,
		"NOMAD_SEAWEEDFS__S3_ENDPOINT="+localEndpoint,
		"NOMAD_SEAWEEDFS__PUBLIC_ENDPOINT="+publicEndpoint,
		"NOMAD_SEAWEEDFS__S3_BUCKET="+localBucket,
		"NOMAD_SEAWEEDFS__S3_ACCESS_KEY="+localAccessKey,
		"NOMAD_SEAWEEDFS__S3_SECRET_KEY="+localSecretKey,
		"NOMAD_SEAWEEDFS__FILER_ENDPOINT="+filerEndpoint,
		"NOMAD_PROVIDERS__CLOUD1__ENDPOINT="+cloudEndpoint,
		"NOMAD_PROVIDERS__CLOUD1__REGION=us-east-1",
		"NOMAD_PROVIDERS__CLOUD1__BUCKET="+cloudBucket,
		"NOMAD_PROVIDERS__CLOUD1__ACCESS_KEY="+cloudAccessKey,
		"NOMAD_PROVIDERS__CLOUD1__SECRET_KEY="+cloudSecretKey,
	)
	command.Stdout = os.Stdout
	command.Stderr = os.Stderr
	if err := command.Start(); err != nil {
		cancel()
		t.Fatalf("start gateway: %v", err)
	}

	t.Cleanup(func() {
		cancel()
		_ = command.Wait()
	})
}

func waitForGateway(ctx context.Context, gatewayURL string) error {
	return waitFor(ctx, func(ctx context.Context) error {
		request, err := http.NewRequestWithContext(ctx, http.MethodGet, gatewayURL+"/health", nil)
		if err != nil {
			return err
		}
		response, err := http.DefaultClient.Do(request)
		if err != nil {
			return err
		}
		defer response.Body.Close()
		if response.StatusCode != http.StatusOK {
			return fmt.Errorf("health status = %d", response.StatusCode)
		}
		return nil
	})
}

func newS3Client(ctx context.Context, endpoint, accessKey, secretKey string) (*s3.Client, error) {
	awsCfg, err := awsconfig.LoadDefaultConfig(
		ctx,
		awsconfig.WithRegion("us-east-1"),
		awsconfig.WithCredentialsProvider(credentials.NewStaticCredentialsProvider(accessKey, secretKey, "")),
	)
	if err != nil {
		return nil, err
	}

	return s3.NewFromConfig(awsCfg, func(options *s3.Options) {
		options.BaseEndpoint = aws.String(endpoint)
		options.UsePathStyle = true
	}), nil
}

func ensureBucket(ctx context.Context, client *s3.Client, bucket string) error {
	_, err := client.CreateBucket(ctx, &s3.CreateBucketInput{Bucket: aws.String(bucket)})
	if err == nil {
		return nil
	}

	var apiErr smithy.APIError
	if errors.As(err, &apiErr) {
		switch apiErr.ErrorCode() {
		case "BucketAlreadyExists", "BucketAlreadyOwnedByYou":
			return nil
		}
	}
	return err
}

func putObject(ctx context.Context, client *s3.Client, bucket, key string, body []byte) error {
	_, err := client.PutObject(ctx, &s3.PutObjectInput{
		Bucket: aws.String(bucket),
		Key:    aws.String(key),
		Body:   bytes.NewReader(body),
	})
	return err
}

func mountRemoteZip(ctx context.Context, uploadID string) error {
	packageRoot := repositoryRoot()
	composeFile := filepath.Join(packageRoot, "e2e", "docker-compose.yaml")
	command := exec.CommandContext(
		ctx,
		"docker", "compose", "-f", composeFile, "exec", "-T", "seaweedfs",
		"weed", "shell", "-filer=localhost:8888",
	)
	command.Dir = packageRoot
	command.Stdin = strings.NewReader(fmt.Sprintf(
		"remote.configure -name=cloud1 -type=s3 -s3.access_key=%s -s3.secret_key=%s -s3.region=us-east-1 -s3.endpoint=http://rustfs:9000 -s3.force_path_style=true\n"+
			"remote.mount -dir=%s -remote=cloud1/%s/%s -metadataStrategy=eager\n",
		cloudAccessKey,
		cloudSecretKey,
		uploadDirectory(localBucket, uploadID),
		cloudBucket,
		uploadID[:2]+"/"+uploadID,
	))

	output, err := command.CombinedOutput()
	if err != nil {
		return fmt.Errorf("run remote setup: %w\n%s", err, output)
	}
	return nil
}

func waitForEntry(ctx context.Context, client filer_pb.SeaweedFilerClient, directory, name string) (*filer_pb.Entry, error) {
	var entry *filer_pb.Entry
	err := waitFor(ctx, func(ctx context.Context) error {
		response, err := client.LookupDirectoryEntry(ctx, &filer_pb.LookupDirectoryEntryRequest{
			Directory: directory,
			Name:      name,
		})
		if err != nil {
			return err
		}
		if response == nil || response.Entry == nil {
			return errors.New("filer returned an empty entry")
		}
		entry = response.Entry
		return nil
	})
	return entry, err
}

func assertDownload(t *testing.T, ctx context.Context, gatewayURL, uploadID, storageEndpoint, bucket string, wantBody []byte) {
	t.Helper()

	request, err := http.NewRequestWithContext(ctx, http.MethodGet, gatewayURL+"/zip/"+url.PathEscape(uploadID), nil)
	if err != nil {
		t.Fatalf("create gateway request: %v", err)
	}
	client := &http.Client{CheckRedirect: func(*http.Request, []*http.Request) error {
		return http.ErrUseLastResponse
	}}
	response, err := client.Do(request)
	if err != nil {
		t.Fatalf("request gateway: %v", err)
	}
	defer response.Body.Close()

	if response.StatusCode != http.StatusTemporaryRedirect {
		body, _ := io.ReadAll(response.Body)
		t.Fatalf("gateway status = %d, want %d; body = %q", response.StatusCode, http.StatusTemporaryRedirect, body)
	}
	location := response.Header.Get("Location")
	redirectURL, err := url.Parse(location)
	if err != nil {
		t.Fatalf("parse presigned URL %q: %v", location, err)
	}
	storageURL, err := url.Parse(storageEndpoint)
	if err != nil {
		t.Fatalf("parse storage endpoint %q: %v", storageEndpoint, err)
	}
	if redirectURL.Host != storageURL.Host {
		t.Errorf("redirect host = %q, want %q", redirectURL.Host, storageURL.Host)
	}
	wantPathPrefix := path.Join("/", storageURL.Path, bucket) + "/"
	if !strings.HasPrefix(redirectURL.Path, wantPathPrefix) {
		t.Errorf("redirect path = %q, want prefix %q", redirectURL.Path, wantPathPrefix)
	}
	if redirectURL.Query().Get("X-Amz-Signature") == "" {
		t.Error("redirect is missing an AWS signature")
	}

	storageRequest, err := http.NewRequestWithContext(ctx, http.MethodGet, location, nil)
	if err != nil {
		t.Fatalf("create storage request: %v", err)
	}
	storageResponse, err := http.DefaultClient.Do(storageRequest)
	if err != nil {
		t.Fatalf("request presigned URL: %v", err)
	}
	defer storageResponse.Body.Close()
	if storageResponse.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(storageResponse.Body)
		t.Fatalf("storage status = %d, want %d; body = %q", storageResponse.StatusCode, http.StatusOK, body)
	}
	gotBody, err := io.ReadAll(storageResponse.Body)
	if err != nil {
		t.Fatalf("read downloaded object: %v", err)
	}
	if !bytes.Equal(gotBody, wantBody) {
		t.Errorf("downloaded body = %q, want %q", gotBody, wantBody)
	}
}

func waitFor(ctx context.Context, operation func(context.Context) error) error {
	ticker := time.NewTicker(500 * time.Millisecond)
	defer ticker.Stop()

	var lastErr error
	for {
		if err := operation(ctx); err == nil {
			return nil
		} else {
			lastErr = err
		}

		select {
		case <-ctx.Done():
			return fmt.Errorf("timed out: %w; last error: %v", ctx.Err(), lastErr)
		case <-ticker.C:
		}
	}
}

func uploadKey(uploadID string) string {
	return uploadID[:2] + "/" + uploadID + "/" + zipFileName
}

func uploadDirectory(bucket, uploadID string) string {
	return fmt.Sprintf("/buckets/%s/%s/%s", bucket, uploadID[:2], uploadID)
}

func envOrDefault(name, fallback string) string {
	if value := os.Getenv(name); value != "" {
		return value
	}
	return fallback
}

func repositoryRoot() string {
	_, file, _, _ := runtime.Caller(0)
	return filepath.Dir(filepath.Dir(file))
}
