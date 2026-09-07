package server

import (
	"context"
	"fmt"
	"io"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/s3"
)

type zipObjectReader struct {
	ctx    context.Context
	client *s3.Client
	bucket string
	key    string
}

func newZipObjectReader(ctx context.Context, client *s3.Client, bucket, key string) *zipObjectReader {
	return &zipObjectReader{
		ctx:    ctx,
		client: client,
		bucket: bucket,
		key:    key,
	}
}

func (r *zipObjectReader) ReadAt(p []byte, off int64) (int, error) {
	if off < 0 {
		return 0, fmt.Errorf("invalid offset: %d", off)
	}
	if len(p) == 0 {
		return 0, nil
	}

	end := off + int64(len(p)) - 1
	response, err := r.client.GetObject(r.ctx, &s3.GetObjectInput{
		Bucket: aws.String(r.bucket),
		Key:    aws.String(r.key),
		Range:  aws.String(fmt.Sprintf("bytes=%d-%d", off, end)),
	})
	if err != nil {
		return 0, err
	}
	defer response.Body.Close()

	return io.ReadFull(response.Body, p)
}
