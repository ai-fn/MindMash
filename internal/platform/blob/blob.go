// Package blob provides an S3/MinIO client with upload, download, delete, and
// presigned URL helpers.
package blob

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"strconv"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/credentials"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/aws/aws-sdk-go-v2/service/s3/types"
)

// Config holds S3/MinIO connection configuration.
type Config struct {
	Endpoint        string // S3_ENDPOINT, default "http://localhost:9000"
	Region          string // S3_REGION, default "us-east-1"
	AccessKeyID     string // S3_ACCESS_KEY_ID, default "minioadmin"
	SecretAccessKey string // S3_SECRET_ACCESS_KEY, default "minioadmin"
	UsePathStyle    bool   // S3_USE_PATH_STYLE, default true
	DefaultBucket   string // S3_DEFAULT_BUCKET, default "mindmesh"
}

// LoadConfig reads blob configuration from environment variables.
// It returns an error when S3_USE_PATH_STYLE is set but cannot be parsed as bool.
func LoadConfig() (Config, error) {
	usePathStyle := true
	if v, ok := os.LookupEnv("S3_USE_PATH_STYLE"); ok {
		b, err := strconv.ParseBool(v)
		if err != nil {
			return Config{}, fmt.Errorf("blob: S3_USE_PATH_STYLE: cannot parse %q as bool: %w", v, err)
		}
		usePathStyle = b
	}

	return Config{
		Endpoint:        envStr("S3_ENDPOINT", "http://localhost:9000"),
		Region:          envStr("S3_REGION", "us-east-1"),
		AccessKeyID:     envStr("S3_ACCESS_KEY_ID", "minioadmin"),
		SecretAccessKey: envStr("S3_SECRET_ACCESS_KEY", "minioadmin"),
		UsePathStyle:    usePathStyle,
		DefaultBucket:   envStr("S3_DEFAULT_BUCKET", "mindmesh"),
	}, nil
}

// Client wraps an AWS S3 client, providing upload, download, delete and presign
// operations compatible with both AWS S3 and MinIO.
type Client struct {
	s3 *s3.Client
}

// NewClient creates a Client configured with static credentials and a custom
// endpoint so that it can target a MinIO instance as well as real AWS S3.
func NewClient(ctx context.Context, cfg Config) (*Client, error) {
	awsCfg, err := config.LoadDefaultConfig(ctx,
		config.WithRegion(cfg.Region),
		config.WithCredentialsProvider(credentials.NewStaticCredentialsProvider(
			cfg.AccessKeyID,
			cfg.SecretAccessKey,
			"",
		)),
		// WithBaseEndpoint redirects all requests to the custom endpoint.
		// This is the recommended approach for MinIO and other S3-compatible stores.
		config.WithBaseEndpoint(cfg.Endpoint),
	)
	if err != nil {
		return nil, fmt.Errorf("blob: load aws config: %w", err)
	}

	c := s3.NewFromConfig(awsCfg, func(o *s3.Options) {
		o.UsePathStyle = cfg.UsePathStyle
	})
	return &Client{s3: c}, nil
}

// Upload stores body at bucket/key with the given content-type.
func (c *Client) Upload(ctx context.Context, bucket, key string, body io.Reader, contentType string) error {
	_, err := c.s3.PutObject(ctx, &s3.PutObjectInput{
		Bucket:      aws.String(bucket),
		Key:         aws.String(key),
		Body:        body,
		ContentType: aws.String(contentType),
	})
	if err != nil {
		return fmt.Errorf("blob: upload %s/%s: %w", bucket, key, err)
	}
	return nil
}

// Download retrieves the object at bucket/key and returns the body stream.
// The caller is responsible for closing the returned io.ReadCloser.
func (c *Client) Download(ctx context.Context, bucket, key string) (io.ReadCloser, error) {
	result, err := c.s3.GetObject(ctx, &s3.GetObjectInput{
		Bucket: aws.String(bucket),
		Key:    aws.String(key),
	})
	if err != nil {
		return nil, fmt.Errorf("blob: download %s/%s: %w", bucket, key, err)
	}
	return result.Body, nil
}

// Delete removes the object at bucket/key.
func (c *Client) Delete(ctx context.Context, bucket, key string) error {
	_, err := c.s3.DeleteObject(ctx, &s3.DeleteObjectInput{
		Bucket: aws.String(bucket),
		Key:    aws.String(key),
	})
	if err != nil {
		return fmt.Errorf("blob: delete %s/%s: %w", bucket, key, err)
	}
	return nil
}

// PresignGet returns a pre-signed GET URL for bucket/key, valid for ttl.
func (c *Client) PresignGet(ctx context.Context, bucket, key string, ttl time.Duration) (string, error) {
	pc := s3.NewPresignClient(c.s3)
	req, err := pc.PresignGetObject(ctx, &s3.GetObjectInput{
		Bucket: aws.String(bucket),
		Key:    aws.String(key),
	}, s3.WithPresignExpires(ttl))
	if err != nil {
		return "", fmt.Errorf("blob: presign get %s/%s: %w", bucket, key, err)
	}
	return req.URL, nil
}

// PresignPut returns a pre-signed PUT URL for bucket/key, valid for ttl.
func (c *Client) PresignPut(ctx context.Context, bucket, key string, ttl time.Duration) (string, error) {
	pc := s3.NewPresignClient(c.s3)
	req, err := pc.PresignPutObject(ctx, &s3.PutObjectInput{
		Bucket: aws.String(bucket),
		Key:    aws.String(key),
	}, s3.WithPresignExpires(ttl))
	if err != nil {
		return "", fmt.Errorf("blob: presign put %s/%s: %w", bucket, key, err)
	}
	return req.URL, nil
}

// EnsureBucket creates bucket if it does not already exist.
// It is safe to call on a bucket that was created by a prior run of the same
// service or by an operator; "already exists" conditions are silently ignored.
func (c *Client) EnsureBucket(ctx context.Context, bucket string) error {
	_, err := c.s3.HeadBucket(ctx, &s3.HeadBucketInput{
		Bucket: aws.String(bucket),
	})
	if err == nil {
		return nil
	}

	// HeadBucket returns a generic error that wraps types.NotFound for a
	// missing bucket; MinIO may return types.NoSuchBucket instead.
	// Attempt to create when we see either sentinel.
	var notFound *types.NotFound
	var noSuchBucket *types.NoSuchBucket
	if !errors.As(err, &notFound) && !errors.As(err, &noSuchBucket) {
		return fmt.Errorf("blob: head bucket %s: %w", bucket, err)
	}

	_, createErr := c.s3.CreateBucket(ctx, &s3.CreateBucketInput{
		Bucket: aws.String(bucket),
	})
	if createErr != nil {
		// Ignore races where another process created the bucket first.
		var alreadyOwned *types.BucketAlreadyOwnedByYou
		var alreadyExists *types.BucketAlreadyExists
		if errors.As(createErr, &alreadyOwned) || errors.As(createErr, &alreadyExists) {
			return nil
		}
		return fmt.Errorf("blob: create bucket %s: %w", bucket, createErr)
	}
	return nil
}

// envStr returns the env var value for key, or def when the variable is unset.
func envStr(key, def string) string {
	if v, ok := os.LookupEnv(key); ok {
		return v
	}
	return def
}
