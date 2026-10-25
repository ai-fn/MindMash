package blob

import (
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"os"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/aws/aws-sdk-go-v2/service/s3/types"
	"github.com/aws/smithy-go"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/mindmesh/mindmesh/internal/platform/testhelpers"
)

func TestLoadConfig_Defaults(t *testing.T) {
	testhelpers.UnsetAll(t,
		"S3_ENDPOINT",
		"S3_REGION",
		"S3_ACCESS_KEY_ID",
		"S3_SECRET_ACCESS_KEY",
		"S3_USE_PATH_STYLE",
		"S3_DEFAULT_BUCKET",
	)

	cfg, err := LoadConfig()
	require.NoError(t, err)

	assert.Equal(t, "http://localhost:9000", cfg.Endpoint)
	assert.Equal(t, "us-east-1", cfg.Region)
	assert.Equal(t, "minioadmin", cfg.AccessKeyID)
	assert.Equal(t, "minioadmin", cfg.SecretAccessKey)
	assert.Equal(t, true, cfg.UsePathStyle)
	assert.Equal(t, "mindmesh", cfg.DefaultBucket)
}

func TestLoadConfig_UsePathStyleParsed(t *testing.T) {
	tests := []struct {
		name    string
		value   string
		want    bool
		wantErr bool
	}{
		{"false string", "false", false, false},
		{"true string", "true", true, false},
		{"zero", "0", false, false},
		{"one", "1", true, false},
		{"invalid", "invalid", false, true},
	}

	for _, tc := range tests {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Setenv("S3_USE_PATH_STYLE", tc.value)
			cfg, err := LoadConfig()
			if tc.wantErr {
				require.Error(t, err)
				assert.Contains(t, err.Error(), "S3_USE_PATH_STYLE")
				return
			}
			require.NoError(t, err)
			assert.Equal(t, tc.want, cfg.UsePathStyle)
		})
	}
}

func TestIsNotFound(t *testing.T) {
	tests := []struct {
		name string
		err  error
		want bool
	}{
		{"types.NotFound", &types.NotFound{}, true},
		{"types.NoSuchKey", &types.NoSuchKey{}, true},
		{"wrapped NotFound", fmt.Errorf("head: %w", &types.NotFound{}), true},
		{"smithy 404", &smithy.GenericAPIError{Code: "404"}, true},
		{"smithy NoSuchKey", &smithy.GenericAPIError{Code: "NoSuchKey"}, true},
		{"other smithy error", &smithy.GenericAPIError{Code: "AccessDenied"}, false},
		{"plain error", errors.New("boom"), false},
		{"nil", nil, false},
	}
	for _, tc := range tests {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			assert.Equal(t, tc.want, isNotFound(tc.err))
		})
	}
}

func TestLoadConfig_EnvOverride(t *testing.T) {
	t.Setenv("S3_ENDPOINT", "http://minio.internal:9000")
	t.Setenv("S3_REGION", "eu-west-1")
	t.Setenv("S3_ACCESS_KEY_ID", "mykey")
	t.Setenv("S3_SECRET_ACCESS_KEY", "mysecret")
	t.Setenv("S3_USE_PATH_STYLE", "false")
	t.Setenv("S3_DEFAULT_BUCKET", "my-bucket")

	cfg, err := LoadConfig()
	require.NoError(t, err)

	assert.Equal(t, "http://minio.internal:9000", cfg.Endpoint)
	assert.Equal(t, "eu-west-1", cfg.Region)
	assert.Equal(t, "mykey", cfg.AccessKeyID)
	assert.Equal(t, "mysecret", cfg.SecretAccessKey)
	assert.Equal(t, false, cfg.UsePathStyle)
	assert.Equal(t, "my-bucket", cfg.DefaultBucket)
}

// Browsers send every signed header on the direct PUT, so the bucket's CORS rule must allow each one.
func TestPresignPut_SignedHeadersAllowedByBucketCORS(t *testing.T) {
	script, err := os.ReadFile("../../../deploy/scripts/s3-media-bucket.sh")
	if err != nil {
		t.Fatal(err)
	}
	m := regexp.MustCompile(`"AllowedHeaders": \[([^\]]*)\]`).FindSubmatch(script)
	if m == nil {
		t.Fatal("no AllowedHeaders in the bucket CORS rule")
	}
	allowed := map[string]bool{}
	for _, h := range strings.Split(string(m[1]), ",") {
		allowed[strings.ToLower(strings.Trim(strings.TrimSpace(h), `"`))] = true
	}

	c, err := NewClient(context.Background(), Config{Endpoint: "http://localhost:9000", Region: "us-east-1",
		AccessKeyID: "a", SecretAccessKey: "b", UsePathStyle: true, DefaultBucket: "m"})
	if err != nil {
		t.Fatal(err)
	}
	sum := sha256.Sum256([]byte("x"))
	p, err := c.PresignPut(context.Background(), "m", "k", time.Minute,
		PutConstraints{ContentType: "image/png", ContentLength: 1, ChecksumSHA256: sum[:]})
	if err != nil {
		t.Fatal(err)
	}
	for h := range p.Headers {
		// The browser sets Content-Length itself; it is never preflighted.
		if lh := strings.ToLower(h); lh != "content-length" && !allowed[lh] {
			t.Errorf("signed header %q is not in the bucket's CORS AllowedHeaders", h)
		}
	}
}
