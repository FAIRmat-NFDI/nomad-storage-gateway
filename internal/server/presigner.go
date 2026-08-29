package server

import (
	"context"

	"github.com/FAIRmat-NFDI/nomad-storage-gateway/internal/config"
	"github.com/aws/aws-sdk-go-v2/aws"
	awsconfig "github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/credentials"
	"github.com/aws/aws-sdk-go-v2/service/s3"
)

func newPresigner(
	ctx context.Context,
	provider config.ObjectStore,
) (*s3.PresignClient, error) {
	region := provider.Region
	if region == "" {
		// aws library always requires a region
		region = "us-east-1"
	}
	awsCfg, err := awsconfig.LoadDefaultConfig(
		ctx,
		awsconfig.WithRegion(region),
		awsconfig.WithCredentialsProvider(
			credentials.NewStaticCredentialsProvider(
				provider.AccessKey,
				provider.SecretKey,
				"",
			),
		),
	)
	if err != nil {
		return nil, err
	}

	client := s3.NewFromConfig(awsCfg, func(options *s3.Options) {
		options.BaseEndpoint = aws.String(provider.Endpoint)
		options.UsePathStyle = true
	})

	return s3.NewPresignClient(client), nil
}
