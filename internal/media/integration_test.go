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
	"strings"
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

	"github.com/mindmesh/mindmesh/internal/platform/apperr"
	"github.com/mindmesh/mindmesh/internal/platform/blob"
	platformdb "github.com/mindmesh/mindmesh/internal/platform/db"
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

	minioCtr, err := minio.Run(ctx, "pgsty/minio:RELEASE.2026-08-04T00-00-00Z@sha256:b6bfe7239bfc83fb90d31612d9704d86039dd714f7904b3f1ad68f211e602372")
	require.NoError(t, err, "start minio")
	t.Cleanup(func() { _ = minioCtr.Terminate(ctx) })

	dsn, err := pgCtr.ConnectionString(ctx, "sslmode=disable")
	require.NoError(t, err)
	require.NoError(t, platformdb.Migrate(ctx, dsn, "../../migrations", "platform", "media"))

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

// putStatus uploads data with the presigned headers, as a browser client would.
func putStatus(t *testing.T, res PresignResult, data []byte) int {
	t.Helper()
	req, err := http.NewRequest(http.MethodPut, res.UploadURL, bytes.NewReader(data))
	require.NoError(t, err)
	for k, v := range res.UploadHeaders {
		req.Header.Set(k, v)
	}
	resp, err := http.DefaultClient.Do(req)
	require.NoError(t, err)
	defer resp.Body.Close()
	return resp.StatusCode
}

func put(t *testing.T, res PresignResult, data []byte) {
	t.Helper()
	require.Less(t, putStatus(t, res, data), 300, "PUT to presigned URL should succeed")
}

// upload presigns, PUTs and confirms data for tn.
func upload(t *testing.T, c components, tn Tenant, data []byte, sum string) *Media {
	t.Helper()
	ctx := context.Background()
	res, err := c.svc.Presign(ctx, tn, "f.png", "image/png", sum, int64(len(data)))
	require.NoError(t, err)
	require.False(t, res.AlreadyExists)
	put(t, res, data)
	m, err := c.svc.Confirm(ctx, tn, res.MediaID)
	require.NoError(t, err)
	return m
}

func objectExists(t *testing.T, c components, key string) bool {
	t.Helper()
	_, found, err := c.blob.Stat(context.Background(), testBucket, key)
	require.NoError(t, err)
	return found
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

	put(t, res, data)

	m, err := c.svc.Confirm(ctx, tA(), res.MediaID)
	require.NoError(t, err)
	assert.Equal(t, StatusReady, m.Status)
	assert.Equal(t, sum, m.ChecksumHex())
	assert.Greater(t, m.USN, int64(0))

	// Drive the worker with the emitted event payload.
	require.NoError(t, c.worker.Handle(ctx, events.Message{Topic: Topic, Value: mediaUploadedEntry(m).Payload}))

	// A thumbnail variant row and object should now exist.
	variantKey := variantKeyFor(userA, m.Checksum, thumbVariant, "png")
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

func TestDedupeStaysWithinUser(t *testing.T) {
	c := setup(t)
	ctx := context.Background()
	data, sum := makePNG(t, 100, 100)
	a := upload(t, c, tA(), data, sum)

	again, err := c.svc.Presign(ctx, tA(), "a.png", "image/png", sum, int64(len(data)))
	require.NoError(t, err)
	assert.True(t, again.AlreadyExists)
	assert.Empty(t, again.UploadURL)

	// Knowing A's hash gives B nothing: B must upload the bytes itself, under its own key.
	resB, err := c.svc.Presign(ctx, tB(), "a.png", "image/png", sum, int64(len(data)))
	require.NoError(t, err)
	assert.False(t, resB.AlreadyExists)
	assert.NotEmpty(t, resB.UploadURL)
	_, err = c.svc.Confirm(ctx, tB(), resB.MediaID)
	assert.True(t, apperr.IsFailedPrecondition(err), "confirm without upload: %v", err)
	_, err = c.svc.ResolveDownloadURL(ctx, tB(), resB.MediaID)
	assert.True(t, apperr.IsNotFound(err), "resolve before upload: %v", err)
	bChecks, err := c.svc.ListChecksums(ctx, tB(), 0)
	require.NoError(t, err)
	assert.Empty(t, bChecks)

	put(t, resB, data)
	b, err := c.svc.Confirm(ctx, tB(), resB.MediaID)
	require.NoError(t, err)
	assert.NotEqual(t, a.S3Key, b.S3Key)
}

func TestDeleteRemovesOnlyOwnBlobs(t *testing.T) {
	c := setup(t)
	ctx := context.Background()
	data, sum := makePNG(t, 120, 90)
	a := upload(t, c, tA(), data, sum)
	b := upload(t, c, tB(), data, sum)
	require.NoError(t, c.worker.Handle(ctx, events.Message{Topic: Topic, Value: mediaUploadedEntry(a).Payload}))
	thumbA := variantKeyFor(userA, a.Checksum, thumbVariant, "png")
	require.True(t, objectExists(t, c, thumbA))

	require.NoError(t, c.svc.Delete(ctx, tA(), a.ID))
	assert.False(t, objectExists(t, c, a.S3Key), "A's original should be removed")
	assert.False(t, objectExists(t, c, thumbA), "A's thumbnail should be removed")
	assert.True(t, objectExists(t, c, b.S3Key), "B's copy of the same bytes must survive")
}

func TestTenantIsolation(t *testing.T) {
	c := setup(t)
	ctx := context.Background()
	data, sum := makePNG(t, 64, 64)

	res, err := c.svc.Presign(ctx, tA(), "x.png", "image/png", sum, int64(len(data)))
	require.NoError(t, err)
	put(t, res, data)
	_, err = c.svc.Confirm(ctx, tA(), res.MediaID)
	require.NoError(t, err)

	// User B cannot see, confirm, resolve, or delete user A's media.
	_, err = c.svc.Confirm(ctx, tB(), res.MediaID)
	assert.True(t, apperr.IsNotFound(err), "confirm: %v", err)
	_, err = c.svc.ResolveDownloadURL(ctx, tB(), res.MediaID)
	assert.True(t, apperr.IsNotFound(err), "resolve: %v", err)
	err = c.svc.Delete(ctx, tB(), res.MediaID)
	assert.True(t, apperr.IsNotFound(err), "delete: %v", err)

	bChecks, err := c.svc.ListChecksums(ctx, tB(), 0)
	require.NoError(t, err)
	assert.Empty(t, bChecks)
}

func TestUploadMustMatchDeclaredBytes(t *testing.T) {
	c := setup(t)
	ctx := context.Background()
	data, sum := makePNG(t, 80, 80)

	t.Run("store rejects a PUT of other bytes or size", func(t *testing.T) {
		res, err := c.svc.Presign(ctx, tA(), "m.png", "image/png", sum, int64(len(data)))
		require.NoError(t, err)
		swapped := bytes.Clone(data)
		swapped[len(swapped)-1] ^= 0xff
		assert.GreaterOrEqual(t, putStatus(t, res, swapped), 400, "substituted bytes")
		assert.GreaterOrEqual(t, putStatus(t, res, data[:len(data)-1]), 400, "truncated bytes")
		_, err = c.svc.Confirm(ctx, tA(), res.MediaID)
		assert.True(t, apperr.IsFailedPrecondition(err), "confirm: %v", err)
	})

	t.Run("confirm rejects an object that bypassed the presigned PUT", func(t *testing.T) {
		_, sumB := makePNG(t, 81, 81)
		res, err := c.svc.Presign(ctx, tB(), "m.png", "image/png", sumB, int64(len(data)))
		require.NoError(t, err)
		m, err := c.repo.GetByID(ctx, userB, res.MediaID)
		require.NoError(t, err)
		require.NoError(t, c.blob.Upload(ctx, testBucket, m.S3Key, bytes.NewReader(data), "image/png"))
		_, err = c.svc.Confirm(ctx, tB(), res.MediaID)
		assert.True(t, apperr.IsFailedPrecondition(err), "unverified bytes: %v", err)
	})

	t.Run("confirm rejects another content type", func(t *testing.T) {
		pngData, pngSum := makePNG(t, 82, 82)
		res, err := c.svc.Presign(ctx, tA(), "m.png", "image/png", pngSum, int64(len(pngData)))
		require.NoError(t, err)
		m, err := c.repo.GetByID(ctx, userA, res.MediaID)
		require.NoError(t, err)
		gif, err := c.blob.PresignPut(ctx, testBucket, m.S3Key, time.Minute, blob.PutConstraints{
			ContentType: "image/gif", ContentLength: m.Bytes, ChecksumSHA256: m.Checksum,
		})
		require.NoError(t, err)
		put(t, PresignResult{UploadURL: gif.URL, UploadHeaders: gif.Headers}, pngData)
		_, err = c.svc.Confirm(ctx, tA(), res.MediaID)
		assert.True(t, apperr.IsFailedPrecondition(err), "content type: %v", err)
	})
}

func TestRepresignPendingUsesNewMime(t *testing.T) {
	c := setup(t)
	ctx := context.Background()
	data, sum := makePNG(t, 40, 40)

	first, err := c.svc.Presign(ctx, tA(), "p", "image/gif", sum, int64(len(data)))
	require.NoError(t, err)
	second, err := c.svc.Presign(ctx, tA(), "p", "image/png", sum, int64(len(data)))
	require.NoError(t, err)
	assert.Equal(t, first.MediaID, second.MediaID)
	assert.Equal(t, first.UploadURL[:strings.Index(first.UploadURL, "?")], second.UploadURL[:strings.Index(second.UploadURL, "?")],
		"the key must not move, or a PUT to the first URL would be stranded")
	put(t, second, data)
	m, err := c.svc.Confirm(ctx, tA(), second.MediaID)
	require.NoError(t, err)
	assert.Equal(t, "image/png", m.Mime)
}

func TestPendingGC(t *testing.T) {
	c := setup(t)
	ctx := context.Background()
	staleData, staleSum := makePNG(t, 50, 50)
	_, freshSum := makePNG(t, 51, 51)
	readyData, readySum := makePNG(t, 52, 52)

	stale, err := c.svc.Presign(ctx, tA(), "s", "image/png", staleSum, int64(len(staleData)))
	require.NoError(t, err)
	put(t, stale, staleData)
	fresh, err := c.svc.Presign(ctx, tA(), "f", "image/png", freshSum, 10)
	require.NoError(t, err)
	ready := upload(t, c, tA(), readyData, readySum)
	_, err = c.pool.Exec(ctx, `UPDATE media SET mtime = now() - interval '2 days' WHERE id IN ($1, $2)`, stale.MediaID, ready.ID)
	require.NoError(t, err)
	staleRow, err := c.repo.GetByID(ctx, userA, stale.MediaID)
	require.NoError(t, err)

	n, err := c.svc.purgePending(ctx)
	require.NoError(t, err)
	assert.Equal(t, 1, n)
	_, err = c.repo.GetByID(ctx, userA, stale.MediaID)
	assert.True(t, apperr.IsNotFound(err), "stale pending row should be gone: %v", err)
	assert.False(t, objectExists(t, c, staleRow.S3Key), "stale upload should be deleted")
	_, err = c.repo.GetByID(ctx, userA, fresh.MediaID)
	assert.NoError(t, err, "fresh pending row must be kept")
	_, err = c.repo.GetByID(ctx, userA, ready.ID)
	assert.NoError(t, err, "ready rows are never collected")
}

func TestDeleteStalePendingKeepsRowsTouchedSinceListing(t *testing.T) {
	c := setup(t)
	ctx := context.Background()
	_, sum := makePNG(t, 53, 53)
	res, err := c.svc.Presign(ctx, tA(), "s", "image/png", sum, 10)
	require.NoError(t, err)
	_, err = c.pool.Exec(ctx, `UPDATE media SET mtime = now() - interval '2 days' WHERE id = $1`, res.MediaID)
	require.NoError(t, err)
	cutoff := time.Now().Add(-pendingTTL)
	stale, err := c.repo.ListStalePending(ctx, cutoff, pendingGCBatch)
	require.NoError(t, err)
	require.Len(t, stale, 1)

	// The client re-presigns after the GC listed the row.
	_, err = c.svc.Presign(ctx, tA(), "s", "image/png", sum, 10)
	require.NoError(t, err)
	key, err := c.repo.DeleteStalePending(ctx, stale[0], cutoff)
	require.NoError(t, err)
	assert.Empty(t, key, "a re-presigned row must survive the GC")
	_, err = c.repo.GetByID(ctx, userA, res.MediaID)
	assert.NoError(t, err)
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
	put(t, res, garbage)
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
