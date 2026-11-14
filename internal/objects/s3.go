package objects

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/credentials"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/aws/aws-sdk-go-v2/service/s3/types"
)

// S3Config is an S3-compatible store.
type S3Config struct {
	Endpoint, Region, Bucket, AccessKey, SecretKey string
	PathStyle                                      bool
}

// S3 stores objects in an S3-compatible bucket.
type S3 struct {
	c      *s3.Client
	bucket string
}

// NewS3 returns an S3 store.
func NewS3(_ context.Context, cfg S3Config) (*S3, error) {
	if cfg.Bucket == "" {
		return nil, errors.New("FETTLE_S3_BUCKET is required for the s3 object store")
	}
	region := cfg.Region
	if region == "" {
		region = "us-east-1"
	}
	opts := s3.Options{
		Region:       region,
		UsePathStyle: cfg.PathStyle,
		Credentials:  credentials.NewStaticCredentialsProvider(cfg.AccessKey, cfg.SecretKey, ""),
	}
	if cfg.Endpoint != "" {
		opts.BaseEndpoint = aws.String(cfg.Endpoint)
	}
	return &S3{c: s3.New(opts), bucket: cfg.Bucket}, nil
}

func (s *S3) Put(ctx context.Context, key string, r io.Reader, size int64) error {
	// The SDK needs a seekable body for signing; payloads and archives are buffered.
	b, err := io.ReadAll(r)
	if err != nil {
		return err
	}
	_, err = s.c.PutObject(ctx, &s3.PutObjectInput{Bucket: &s.bucket, Key: &key, Body: bytes.NewReader(b), ContentLength: aws.Int64(int64(len(b)))})
	return err
}

func (s *S3) Get(ctx context.Context, key string) (io.ReadCloser, error) {
	out, err := s.c.GetObject(ctx, &s3.GetObjectInput{Bucket: &s.bucket, Key: &key})
	if err != nil {
		var nk *types.NoSuchKey
		if errors.As(err, &nk) {
			return nil, ErrNotFound
		}
		return nil, err
	}
	return out.Body, nil
}

func (s *S3) Delete(ctx context.Context, key string) error {
	_, err := s.c.DeleteObject(ctx, &s3.DeleteObjectInput{Bucket: &s.bucket, Key: &key})
	return err
}

func (s *S3) List(ctx context.Context, prefix string) ([]string, error) {
	var keys []string
	p := s3.NewListObjectsV2Paginator(s.c, &s3.ListObjectsV2Input{Bucket: &s.bucket, Prefix: &prefix})
	for p.HasMorePages() {
		page, err := p.NextPage(ctx)
		if err != nil {
			return nil, err
		}
		for _, o := range page.Contents {
			keys = append(keys, aws.ToString(o.Key))
		}
	}
	return keys, nil
}

func (s *S3) Check(ctx context.Context) error {
	const k = ".health-check"
	if err := s.Put(ctx, k, bytes.NewReader([]byte("ok")), 2); err != nil {
		return fmt.Errorf("object store is not writable: %w", err)
	}
	return s.Delete(ctx, k)
}
