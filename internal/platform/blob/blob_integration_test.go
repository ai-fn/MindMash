//go:build integration

package blob

import (
	"bytes"
	"context"
	"crypto/sha256"
	"net/http"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/testcontainers/testcontainers-go/modules/minio"
)

const minioImage = "pgsty/minio:RELEASE.2026-08-04T00-00-00Z@sha256:b6bfe7239bfc83fb90d31612d9704d86039dd714f7904b3f1ad68f211e602372"

func TestPresignPutConstraints(t *testing.T) {
	ctx := context.Background()
	ctr, err := minio.Run(ctx, minioImage)
	require.NoError(t, err)
	t.Cleanup(func() { _ = ctr.Terminate(ctx) })
	endpoint, err := ctr.ConnectionString(ctx)
	require.NoError(t, err)
	c, err := NewClient(ctx, Config{
		Endpoint: "http://" + endpoint, Region: "us-east-1",
		AccessKeyID: ctr.Username, SecretAccessKey: ctr.Password, UsePathStyle: true,
	})
	require.NoError(t, err)
	const bucket = "test"
	require.NoError(t, c.EnsureBucket(ctx, bucket))

	data := []byte("the declared bytes")
	sum := sha256.Sum256(data)
	pc := PutConstraints{ContentType: "image/png", ContentLength: int64(len(data)), ChecksumSHA256: sum[:]}

	put := func(t *testing.T, key string, body []byte, mutate func(http.Header)) int {
		t.Helper()
		p, err := c.PresignPut(ctx, bucket, key, time.Minute, pc)
		require.NoError(t, err)
		req, err := http.NewRequest(http.MethodPut, p.URL, bytes.NewReader(body))
		require.NoError(t, err)
		for k, v := range p.Headers {
			req.Header.Set(k, v)
		}
		if mutate != nil {
			mutate(req.Header)
		}
		resp, err := http.DefaultClient.Do(req)
		require.NoError(t, err)
		_ = resp.Body.Close()
		return resp.StatusCode
	}
	stored := func(t *testing.T, key string) bool {
		t.Helper()
		_, found, err := c.Stat(ctx, bucket, key)
		require.NoError(t, err)
		return found
	}

	t.Run("matching upload is stored with its checksum", func(t *testing.T) {
		require.Less(t, put(t, "ok", data, nil), 300)
		info, found, err := c.Stat(ctx, bucket, "ok")
		require.NoError(t, err)
		require.True(t, found)
		assert.Equal(t, sum[:], info.ChecksumSHA256)
		assert.Equal(t, "image/png", info.ContentType)
		assert.Equal(t, int64(len(data)), info.Size)
	})
	t.Run("substituted bytes of the same length are rejected", func(t *testing.T) {
		other := bytes.Repeat([]byte("x"), len(data))
		assert.GreaterOrEqual(t, put(t, "swap", other, nil), 400)
		assert.False(t, stored(t, "swap"))
	})
	t.Run("another content type is rejected", func(t *testing.T) {
		assert.GreaterOrEqual(t, put(t, "type", data, func(h http.Header) { h.Set("Content-Type", "text/html") }), 400)
		assert.False(t, stored(t, "type"))
	})
	t.Run("omitting the checksum header is rejected", func(t *testing.T) {
		assert.GreaterOrEqual(t, put(t, "nosum", data, func(h http.Header) { h.Del("X-Amz-Checksum-Sha256") }), 400)
		assert.False(t, stored(t, "nosum"))
	})
	t.Run("another length is rejected", func(t *testing.T) {
		longer := append(append([]byte{}, data...), '!')
		assert.GreaterOrEqual(t, put(t, "len", longer, nil), 400)
		assert.False(t, stored(t, "len"))
	})
}
