package config

import (
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/knadh/koanf/parsers/yaml"
	"github.com/knadh/koanf/providers/env"
	"github.com/knadh/koanf/providers/file"
	"github.com/knadh/koanf/v2"
)

// ObjectStore defines an external S3-compatible cloud storage target (MPCDF, Wasabi, AWS, etc.)
type ObjectStore struct {
	Type      string `json:"type"`     // "s3"
	Endpoint  string `json:"endpoint"` // "https://s3.eu-central-1.wasabisys.com"
	Region    string `json:"region"`   // "eu-central-1"
	Bucket    string `json:"bucket"`   // "nomad-published-eu"
	AccessKey string `json:"access_key"`
	SecretKey string `json:"secret_key"`
}

// SeaweedFSConfig holds the connection details for the internal SeaweedFS cluster
type SeaweedFSConfig struct {
	// S3 Gateway endpoint for range-reading local volume data
	S3Endpoint  string `json:"s3_endpoint"` // e.g. "http://seaweedfs-s3:8333"
	S3Bucket    string `json:"s3_bucket"`   // e.g. "nomad-public"
	S3AccessKey string `json:"s3_access_key"`
	S3SecretKey string `json:"s3_secret_key"`

	// Filer gRPC address for direct metadata lookup
	FilerEndpoint string `json:"filer_endpoint"` // e.g. "seaweedfs-filer:18888"

	// Public Endpoint for the cluster
	PublicEndpoint string `json:"public_endpoint"` // e.g. "https://nomad-lab.eu/files"

	// NOMAD upload prefix size. This should match the prefix size used in the deployment.
	PrefixSize int `json:"prefix_size"`
}

type Config struct {
	// Server settings
	Port         int           `json:"port"`
	ReadTimeout  time.Duration `json:"read_timeout"`
	WriteTimeout time.Duration `json:"write_timeout"`
	IdleTimeout  time.Duration `json:"idle_timeout"`

	// Internal cluster storage (SeaweedFS)
	SeaweedFS SeaweedFSConfig `json:"seaweedfs"`

	// External cloud storage targets for presigned 307 redirects
	Providers map[string]ObjectStore `json:"providers"`
}

func (c Config) Validate() error {
	var errs []error

	if c.Port < 1 || c.Port > 65535 {
		errs = append(errs, fmt.Errorf("port must be between 1 and 65535"))
	}

	if c.SeaweedFS.S3Endpoint == "" {
		errs = append(errs, errors.New("seaweedfs.s3_endpoint is required"))
	}

	if c.SeaweedFS.S3Bucket == "" {
		errs = append(errs, errors.New("seaweedfs.s3_bucket is required"))
	}

	if c.SeaweedFS.S3AccessKey == "" {
		errs = append(errs, errors.New("seaweedfs.s3_access_key is required"))
	}

	if c.SeaweedFS.S3SecretKey == "" {
		errs = append(errs, errors.New("seaweedfs.s3_secret_key is required"))
	}

	if c.SeaweedFS.FilerEndpoint == "" {
		errs = append(errs, errors.New("seaweedfs.filer_endpoint is required"))
	}

	if c.SeaweedFS.PublicEndpoint == "" {
		errs = append(errs, errors.New("seaweedfs.public_endpoint is required"))
	}

	for name, provider := range c.Providers {
		if provider.Type != "" && provider.Type != "s3" {
			errs = append(errs, fmt.Errorf("providers.%s.type must be %q", name, "s3"))
		}
		if provider.Endpoint == "" {
			errs = append(errs, fmt.Errorf("providers.%s.endpoint is required", name))
		}
		if provider.Bucket == "" {
			errs = append(errs, fmt.Errorf("providers.%s.bucket is required", name))
		}
		if provider.AccessKey == "" {
			errs = append(errs, fmt.Errorf("providers.%s.access_key is required", name))
		}
		if provider.SecretKey == "" {
			errs = append(errs, fmt.Errorf("providers.%s.secret_key is required", name))
		}
	}

	return errors.Join(errs...)
}

func Load(path string) (Config, error) {
	k := koanf.New(".")

	if err := k.Load(file.Provider(path), yaml.Parser()); err != nil {
		return Config{}, fmt.Errorf("load config: %w", err)
	}

	// Environment variables override values from the config file.
	if err := k.Load(env.Provider("NOMAD_", ".", func(key string) string {
		key = strings.TrimPrefix(key, "NOMAD_")
		key = strings.ToLower(key)

		// Double underscore represents nesting.
		// NOMAD_SEAWEEDFS__S3_ENDPOINT
		// becomes seaweedfs.s3_endpoint.
		return strings.ReplaceAll(key, "__", ".")
	}), nil); err != nil {
		return Config{}, fmt.Errorf("load environment: %w", err)
	}

	var cfg Config

	if err := k.UnmarshalWithConf("", &cfg, koanf.UnmarshalConf{Tag: "json"}); err != nil {
		return Config{}, fmt.Errorf("decode config: %w", err)
	}

	if cfg.Port == 0 {
		cfg.Port = 3333
	}

	if err := cfg.Validate(); err != nil {
		return Config{}, fmt.Errorf("invalid config: %w", err)
	}

	if cfg.SeaweedFS.PrefixSize <= 0 {
		cfg.SeaweedFS.PrefixSize = 2
	}

	return cfg, nil
}
