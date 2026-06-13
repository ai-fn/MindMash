package blob

import (
	"testing"

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
