//go:build integration

package media

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"image"
	"image/color"
	"image/png"
	"io"
	"net/http"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/testcontainers/testcontainers-go"
	"github.com/testcontainers/testcontainers-go/modules/minio"
	"github.com/testcontainers/testcontainers-go/modules/postgres"
	"github.com/testcontainers/testcontainers-go/wait"
	"go.uber.org/zap"

	"github.com/mindmesh/mindmesh/internal/platform/blob"
	platformdb "github.com/mindmesh/mindmesh/internal/platform/db"
	platformerrors "github.com/mindmesh/mindmesh/internal/platform/errors"
	"github.com/mindmesh/mindmesh/internal/platform/events"
)

const (
	testBucket = "mindmesh-test"
	userA      = "00000000-0000-0000-0000-00000000000a"
	profA      = "00000000-0000-0000-0000-0000000000fa"
	userB      = "00000000-0000-0000-0000-00000000000b"
	profB      = "00000000-0000-0000-0000-0000000000fb"
)

type components struct {
	pool   *pgxpool.Pool
	blob   *blob.Client
	svc    *Service
	worker *Worker
	repo   *Repo
}

func setup(t *testing.T) components {
	t.Helper()
	ctx := context.Background()

	pgCtr, err := postgres.Run(ctx, "postgres:18-alpine",
		postgres.WithDatabase("media_test"),
		postgres.WithUsername("test"),
		postgres.WithPassword("test"),
		testcontainers.WithWaitStrategy(
			wait.ForLog("database system is ready to accept connections").
				WithOccurrence(2).WithStartupTimeout(2*time.Minute),
		),
	)
	require.NoError(t, err, "start postgres")
	t.Cleanup(func() { _ = pgCtr.Terminate(ctx) })

	minioCtr, err := minio.Run(ctx, "minio/minio:RELEASE.2025-04-22T22-12-26Z")
	require.NoError(t, err, "start minio")
	t.Cleanup(func() { _ = minioCtr.Terminate(ctx) })

	dsn, err := pgCtr.ConnectionString(ctx, "sslmode=disable")
	require.NoError(t, err)
	require.NoError(t, platformdb.RunMigrations(ctx, dsn, "../../migrations/media"))

	pool, err := pgxpool.New(ctx, dsn)
	require.NoError(t, err)
	t.Cleanup(pool.Close)

	endpoint, err := minioCtr.ConnectionString(ctx)
	require.NoError(t, err)
	blobClient, err := blob.NewClient(ctx, blob.Config{
		Endpoint:        "http://" + endpoint,
		Region:          "us-east-1",
		AccessKeyID:     minioCtr.Username,
		SecretAccessKey: minioCtr.Password,
		UsePathStyle:    true,
		DefaultBucket:   testBucket,
	})
	require.NoError(t, err)
	require.NoError(t, blobClient.EnsureBucket(ctx, testBucket))

	repo := NewRepo(pool)
	svc := NewService(repo, blobClient, testBucket, zap.NewNop())
	worker := NewWorker(repo, blobClient, testBucket, zap.NewNop())
	return components{pool: pool, blob: blobClient, svc: svc, worker: worker, repo: repo}
}

// makePNG returns a small PNG (with alpha) and its lowercase-hex SHA-256.
func makePNG(t *testing.T, w, h int) ([]byte, string) {
	t.Helper()
	img := image.NewRGBA(image.Rect(0, 0, w, h))
	for x := 0; x < w; x++ {
		for y := 0; y < h; y++ {
			img.Set(x, y, color.RGBA{uint8(x % 256), uint8(y % 256), 128, 200})
		}
	}
	var buf bytes.Buffer
	require.NoError(t, png.Encode(&buf, img))
	data := buf.Bytes()
	sum := sha256.Sum256(data)
	return data, hex.EncodeToString(sum[:])
}

// put uploads data to a presigned PUT URL, as a browser client would.
func put(t *testing.T, url string, data []byte, contentType string) {
	t.Helper()
	req, err := http.NewRequest(http.MethodPut, url, bytes.NewReader(data))
	require.NoError(t, err)
	req.Header.Set("Content-Type", contentType)
	resp, err := http.DefaultClient.Do(req)
	require.NoError(t, err)
	defer resp.Body.Close()
	require.Less(t, resp.StatusCode, 300, "PUT to presigned URL should succeed")
}

func tA() Tenant { return Tenant{UserID: userA, ProfileID: profA} }
func tB() Tenant { return Tenant{UserID: userB, ProfileID: profB} }

func TestPresignConfirmWorkerFlow(t *testing.T) {
	c := setup(t)
	ctx := context.Background()
	data, sum := makePNG(t, 600, 400)

	res, err := c.svc.Presign(ctx, tA(), "pic.png", "image/png", sum, int64(len(data)))
	require.NoError(t, err)
	require.False(t, res.AlreadyExists)
	require.NotEmpty(t, res.UploadURL)
	require.NotEmpty(t, res.MediaID)

	put(t, res.UploadURL, data, "image/png")

	m, err := c.svc.Confirm(ctx, tA(), res.MediaID)
	require.NoError(t, err)
	assert.Equal(t, StatusReady, m.Status)
	assert.Equal(t, sum, m.ChecksumHex())
	assert.Greater(t, m.USN, int64(0))

	// Drive the worker with the emitted event payload.
	require.NoError(t, c.worker.Handle(ctx, events.Message{Topic: Topic, Value: mediaUploadedEntry(m).Payload}))

	// A thumbnail variant row and object should now exist.
	variantKey := variantKeyFor(m.Checksum, thumbVariant, "png")
	_, found, err := c.blob.Stat(ctx, testBucket, variantKey)
	require.NoError(t, err)
	assert.True(t, found, "thumbnail object should be uploaded")

	var count int
	require.NoError(t, c.pool.QueryRow(ctx,
		`SELECT count(*) FROM media_variants WHERE user_id=$1 AND media_id=$2 AND variant=$3`,
		userA, m.ID, thumbVariant).Scan(&count))
	assert.Equal(t, 1, count)

	// Worker is idempotent.
	require.NoError(t, c.worker.Handle(ctx, events.Message{Topic: Topic, Value: mediaUploadedEntry(m).Payload}))

	// Download resolves to a working presigned GET URL.
	url, err := c.svc.ResolveDownloadURL(ctx, tA(), res.MediaID)
	require.NoError(t, err)
	require.NotEmpty(t, url)
	resp, err := http.Get(url)
	require.NoError(t, err)
	defer resp.Body.Close()
	got, err := io.ReadAll(resp.Body)
	require.NoError(t, err)
	assert.Equal(t, data, got)
}

func TestDedupeAcrossUsers(t *testing.T) {
	c := setup(t)
	ctx := context.Background()
	data, sum := makePNG(t, 100, 100)

	// User A uploads and confirms.
	res, err := c.svc.Presign(ctx, tA(), "a.png", "image/png", sum, int64(len(data)))
	require.NoError(t, err)
	put(t, res.UploadURL, data, "image/png")
	_, err = c.svc.Confirm(ctx, tA(), res.MediaID)
	require.NoError(t, err)

	// Same user, same checksum → dedupe, no upload URL.
	again, err := c.svc.Presign(ctx, tA(), "a.png", "image/png", sum, int64(len(data)))
	require.NoError(t, err)
	assert.True(t, again.AlreadyExists)
	assert.Empty(t, again.UploadURL)

	// Different user, same bytes already in S3 → dedupe short-circuit, own ready row.
	resB, err := c.svc.Presign(ctx, tB(), "a.png", "image/png", sum, int64(len(data)))
	require.NoError(t, err)
	assert.True(t, resB.AlreadyExists)
	assert.Empty(t, resB.UploadURL)

	bChecks, err := c.svc.ListChecksums(ctx, tB(), 0)
	require.NoError(t, err)
	require.Len(t, bChecks, 1)
	assert.Equal(t, sum, hex.EncodeToString(bChecks[0].Checksum))
	assert.Equal(t, StatusReady, bChecks[0].Status)
}

func TestDeleteGarbageCollectsSharedBlob(t *testing.T) {
	c := setup(t)
	ctx := context.Background()
	data, sum := makePNG(t, 120, 90)
	key := s3KeyFor(mustHex(t, sum), "png")

	// A uploads+confirms; B dedupes onto the same blob.
	resA, err := c.svc.Presign(ctx, tA(), "s.png", "image/png", sum, int64(len(data)))
	require.NoError(t, err)
	put(t, resA.UploadURL, data, "image/png")
	_, err = c.svc.Confirm(ctx, tA(), resA.MediaID)
	require.NoError(t, err)
	resB, err := c.svc.Presign(ctx, tB(), "s.png", "image/png", sum, int64(len(data)))
	require.NoError(t, err)

	// Deleting A leaves the blob (B still references it).
	require.NoError(t, c.svc.Delete(ctx, tA(), resA.MediaID))
	_, found, err := c.blob.Stat(ctx, testBucket, key)
	require.NoError(t, err)
	assert.True(t, found, "blob should survive while B references it")

	// Deleting B (the last reference) GCs the blob.
	require.NoError(t, c.svc.Delete(ctx, tB(), resB.MediaID))
	_, found, err = c.blob.Stat(ctx, testBucket, key)
	require.NoError(t, err)
	assert.False(t, found, "blob should be GC'd once unreferenced")
}

func TestTenantIsolation(t *testing.T) {
	c := setup(t)
	ctx := context.Background()
	data, sum := makePNG(t, 64, 64)

	res, err := c.svc.Presign(ctx, tA(), "x.png", "image/png", sum, int64(len(data)))
	require.NoError(t, err)
	put(t, res.UploadURL, data, "image/png")
	_, err = c.svc.Confirm(ctx, tA(), res.MediaID)
	require.NoError(t, err)

	// User B cannot see, confirm, resolve, or delete user A's media.
	_, err = c.svc.Confirm(ctx, tB(), res.MediaID)
	assert.True(t, platformerrors.IsNotFound(err), "confirm: %v", err)
	_, err = c.svc.ResolveDownloadURL(ctx, tB(), res.MediaID)
	assert.True(t, platformerrors.IsNotFound(err), "resolve: %v", err)
	err = c.svc.Delete(ctx, tB(), res.MediaID)
	assert.True(t, platformerrors.IsNotFound(err), "delete: %v", err)

	bChecks, err := c.svc.ListChecksums(ctx, tB(), 0)
	require.NoError(t, err)
	assert.Empty(t, bChecks)
}

func TestConfirmSizeMismatch(t *testing.T) {
	c := setup(t)
	ctx := context.Background()
	data, sum := makePNG(t, 80, 80)

	// Declare a wrong (larger) size; the object HEAD on confirm must reject it.
	res, err := c.svc.Presign(ctx, tA(), "m.png", "image/png", sum, int64(len(data)+999))
	require.NoError(t, err)
	put(t, res.UploadURL, data, "image/png")
	_, err = c.svc.Confirm(ctx, tA(), res.MediaID)
	require.Error(t, err)
	assert.True(t, platformerrors.IsFailedPrecondition(err), "want FailedPrecondition, got %v", err)
}

func TestWorkerSkipsUndecodableImage(t *testing.T) {
	c := setup(t)
	ctx := context.Background()

	// Bytes declared as image/png but not actually a decodable image.
	garbage := []byte("this is definitely not a PNG, but it has a size")
	sum := sha256.Sum256(garbage)
	sumHex := hex.EncodeToString(sum[:])

	res, err := c.svc.Presign(ctx, tA(), "bad.png", "image/png", sumHex, int64(len(garbage)))
	require.NoError(t, err)
	put(t, res.UploadURL, garbage, "image/png")
	m, err := c.svc.Confirm(ctx, tA(), res.MediaID)
	require.NoError(t, err)

	// The worker must ACK (return nil) — retrying an undecodable image is futile —
	// and must not record a variant.
	require.NoError(t, c.worker.Handle(ctx, events.Message{Topic: Topic, Value: mediaUploadedEntry(m).Payload}))

	var count int
	require.NoError(t, c.pool.QueryRow(ctx,
		`SELECT count(*) FROM media_variants WHERE user_id=$1 AND media_id=$2`, userA, m.ID).Scan(&count))
	assert.Equal(t, 0, count, "no variant should be recorded for an undecodable image")
}

func mustHex(t *testing.T, s string) []byte {
	t.Helper()
	b, err := hex.DecodeString(s)
	require.NoError(t, err)
	return b
}
