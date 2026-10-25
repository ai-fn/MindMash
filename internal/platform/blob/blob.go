// Package blob provides an S3/MinIO client with upload, download, delete, and
// presigned URL helpers.
package blob

import (
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"io"
	"strings"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	v4 "github.com/aws/aws-sdk-go-v2/aws/signer/v4"
	awsconfig "github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/credentials"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/aws/aws-sdk-go-v2/service/s3/types"
	"github.com/aws/smithy-go"

	"github.com/mindmesh/mindmesh/internal/platform/config"
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
	usePathStyle, err := config.Bool("S3_USE_PATH_STYLE", true)
	if err != nil {
		return Config{}, fmt.Errorf("blob: %w", err)
	}

	return Config{
		Endpoint:        config.Str("S3_ENDPOINT", "http://localhost:9000"),
		Region:          config.Str("S3_REGION", "us-east-1"),
		AccessKeyID:     config.Str("S3_ACCESS_KEY_ID", "minioadmin"),
		SecretAccessKey: config.Str("S3_SECRET_ACCESS_KEY", "minioadmin"),
		UsePathStyle:    usePathStyle,
		DefaultBucket:   config.Str("S3_DEFAULT_BUCKET", "mindmesh"),
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
	awsCfg, err := awsconfig.LoadDefaultConfig(ctx,
		awsconfig.WithRegion(cfg.Region),
		awsconfig.WithCredentialsProvider(credentials.NewStaticCredentialsProvider(
			cfg.AccessKeyID,
			cfg.SecretAccessKey,
			"",
		)),
		// WithBaseEndpoint redirects all requests to the custom endpoint.
		// This is the recommended approach for MinIO and other S3-compatible stores.
		awsconfig.WithBaseEndpoint(cfg.Endpoint),
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

// PutConstraints pins what a presigned PUT may store; zero fields stay unsigned.
type PutConstraints struct {
	ContentType    string
	ContentLength  int64
	ChecksumSHA256 []byte // raw digest
}

// PresignedPut is a presigned PUT; the store rejects it unless the client sends Headers verbatim.
type PresignedPut struct {
	URL     string
	Headers map[string]string
}

// PresignPut returns a pre-signed PUT for bucket/key, valid for ttl and bound to pc.
func (c *Client) PresignPut(ctx context.Context, bucket, key string, ttl time.Duration, pc PutConstraints) (PresignedPut, error) {
	in := &s3.PutObjectInput{Bucket: aws.String(bucket), Key: aws.String(key)}
	if pc.ContentType != "" {
		in.ContentType = aws.String(pc.ContentType)
	}
	if pc.ContentLength > 0 {
		in.ContentLength = aws.Int64(pc.ContentLength)
	}
	if len(pc.ChecksumSHA256) > 0 {
		in.ChecksumSHA256 = aws.String(base64.StdEncoding.EncodeToString(pc.ChecksumSHA256))
	}
	req, err := s3.NewPresignClient(c.s3, func(o *s3.PresignOptions) {
		// MinIO verifies x-amz-checksum-sha256 only as a header, not once hoisted into the query.
		o.Presigner = v4.NewSigner(func(so *v4.SignerOptions) {
			so.DisableURIPathEscaping = true
			so.DisableHeaderHoisting = true
		})
	}).PresignPutObject(ctx, in, s3.WithPresignExpires(ttl))
	if err != nil {
		return PresignedPut{}, fmt.Errorf("blob: presign put %s/%s: %w", bucket, key, err)
	}
	headers := make(map[string]string, len(req.SignedHeader))
	for k, v := range req.SignedHeader {
		if !strings.EqualFold(k, "Host") {
			headers[k] = strings.Join(v, ",")
		}
	}
	return PresignedPut{URL: req.URL, Headers: headers}, nil
}

// ObjectInfo describes a stored object, as returned by Stat.
type ObjectInfo struct {
	Size           int64
	ContentType    string
	ETag           string
	ChecksumSHA256 []byte // nil unless the object was stored with a SHA-256 checksum
}

// Stat issues a HEAD request for bucket/key. It returns found=false (and a nil
// error) when the object does not exist, so callers can distinguish "absent"
// from a genuine transport/permission failure. This backs the Media confirm
// flow, which HEADs a client-uploaded object to verify it landed and to read
// its size before recording metadata.
func (c *Client) Stat(ctx context.Context, bucket, key string) (ObjectInfo, bool, error) {
	out, err := c.s3.HeadObject(ctx, &s3.HeadObjectInput{
		Bucket:       aws.String(bucket),
		Key:          aws.String(key),
		ChecksumMode: types.ChecksumModeEnabled,
	})
	if err != nil {
		if isNotFound(err) {
			return ObjectInfo{}, false, nil
		}
		return ObjectInfo{}, false, fmt.Errorf("blob: stat %s/%s: %w", bucket, key, err)
	}

	info := ObjectInfo{}
	if out.ContentLength != nil {
		info.Size = *out.ContentLength
	}
	if out.ContentType != nil {
		info.ContentType = *out.ContentType
	}
	if out.ETag != nil {
		info.ETag = *out.ETag
	}
	if out.ChecksumSHA256 != nil {
		if sum, err := base64.StdEncoding.DecodeString(*out.ChecksumSHA256); err == nil {
			info.ChecksumSHA256 = sum
		}
	}
	return info, true, nil
}

// isNotFound reports whether err represents an S3/MinIO "object not found"
// condition. HeadObject models this as types.NotFound; MinIO and some proxies
// surface types.NoSuchKey or a bare 404 smithy APIError instead.
func isNotFound(err error) bool {
	var notFound *types.NotFound
	var noSuchKey *types.NoSuchKey
	if errors.As(err, &notFound) || errors.As(err, &noSuchKey) {
		return true
	}
	var apiErr smithy.APIError
	if errors.As(err, &apiErr) {
		switch apiErr.ErrorCode() {
		case "NotFound", "NoSuchKey", "404":
			return true
		}
	}
	return false
}

// CheckBucket reports whether bucket is reachable, without creating it (readiness).
func (c *Client) CheckBucket(ctx context.Context, bucket string) error {
	if _, err := c.s3.HeadBucket(ctx, &s3.HeadBucketInput{Bucket: aws.String(bucket)}); err != nil {
		return fmt.Errorf("blob: head bucket %s: %w", bucket, err)
	}
	return nil
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
