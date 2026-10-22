package media

import (
	"bytes"
	"context"
	"io"
	"time"

	"go.uber.org/zap"

	"github.com/mindmesh/mindmesh/internal/platform/blob"
	platformerrors "github.com/mindmesh/mindmesh/internal/platform/errors"
	"github.com/mindmesh/mindmesh/internal/platform/ids"
)

// blobStore is the subset of *blob.Client the service uses, narrowed to an
// interface so the service can be unit-tested with a fake.
type blobStore interface {
	Stat(ctx context.Context, bucket, key string) (blob.ObjectInfo, bool, error)
	PresignPut(ctx context.Context, bucket, key string, ttl time.Duration, pc blob.PutConstraints) (blob.PresignedPut, error)
	PresignGet(ctx context.Context, bucket, key string, ttl time.Duration) (string, error)
	Upload(ctx context.Context, bucket, key string, body io.Reader, contentType string) error
	Download(ctx context.Context, bucket, key string) (io.ReadCloser, error)
	Delete(ctx context.Context, bucket, key string) error
}

// Tenant is the caller identity every operation is scoped to.
type Tenant struct {
	UserID    string
	ProfileID string
}

// Service orchestrates the repo, the object store, and event emission.
type Service struct {
	repo   *Repo
	blob   blobStore
	bucket string
	log    *zap.Logger
}

// NewService wires the media service.
func NewService(repo *Repo, b blobStore, bucket string, log *zap.Logger) *Service {
	return &Service{repo: repo, blob: b, bucket: bucket, log: log}
}

// PresignResult is the outcome of a presign call.
type PresignResult struct {
	MediaID       string
	UploadURL     string
	UploadHeaders map[string]string
	AlreadyExists bool
}

// Presign validates the request and returns a presigned upload bound to the declared bytes,
// unless the caller already stored them (dedupe never crosses users).
func (s *Service) Presign(ctx context.Context, t Tenant, filename, mime, sha256Hex string, size int64) (PresignResult, error) {
	req, err := ValidateUpload(mime, sha256Hex, size)
	if err != nil {
		return PresignResult{}, err
	}
	m := &Media{
		UserID:    t.UserID,
		ProfileID: t.ProfileID,
		Checksum:  req.Checksum,
		Kind:      req.Kind,
		Mime:      req.Mime,
		S3Key:     s3KeyFor(t.UserID, req.Checksum),
		Bytes:     req.Bytes,
	}

	for attempt := 0; ; attempt++ {
		existing, err := s.repo.FindByChecksum(ctx, t.UserID, req.Checksum)
		if err != nil {
			return PresignResult{}, err
		}
		switch {
		case existing != nil && existing.Status == StatusReady:
			return PresignResult{MediaID: existing.ID, AlreadyExists: true}, nil
		case existing != nil:
			// Re-presign a pending row with this request's mime; bumping mtime keeps the pending GC off it.
			m.ID = existing.ID
			err = s.repo.RefreshPending(ctx, m)
		default:
			m.ID = ids.NewUUID()
			err = s.repo.InsertPending(ctx, m)
		}
		// A concurrent Presign, Confirm or GC moved the row between the read and the write: re-read once.
		if attempt == 0 && (platformerrors.IsNotFound(err) || platformerrors.IsAlreadyExists(err)) {
			continue
		}
		if err != nil {
			return PresignResult{}, err
		}
		break
	}

	put, err := s.blob.PresignPut(ctx, s.bucket, m.S3Key, presignTTL, blob.PutConstraints{
		ContentType: m.Mime, ContentLength: m.Bytes, ChecksumSHA256: m.Checksum,
	})
	if err != nil {
		return PresignResult{}, platformerrors.Unavailable("object store unavailable", err)
	}
	return PresignResult{MediaID: m.ID, UploadURL: put.URL, UploadHeaders: put.Headers}, nil
}

// Confirm verifies the client PUT stored exactly the declared bytes, then flips
// the row to ready and emits media.uploaded. It is idempotent.
func (s *Service) Confirm(ctx context.Context, t Tenant, mediaID string) (*Media, error) {
	m, err := s.repo.GetByID(ctx, t.UserID, mediaID)
	if err != nil {
		return nil, err
	}
	if m.Status == StatusReady {
		return m, nil // already confirmed
	}

	info, found, err := s.blob.Stat(ctx, s.bucket, m.S3Key)
	if err != nil {
		return nil, platformerrors.Unavailable("object store unavailable", err)
	}
	switch {
	case !found:
		return nil, platformerrors.FailedPrecondition("upload not found; PUT the bytes before confirming")
	case info.Size != m.Bytes:
		return nil, platformerrors.FailedPrecondition("uploaded size does not match the declared size")
	case !bytes.Equal(info.ChecksumSHA256, m.Checksum):
		return nil, platformerrors.FailedPrecondition("uploaded bytes do not match the declared sha256")
	case info.ContentType != m.Mime:
		return nil, platformerrors.FailedPrecondition("uploaded content type does not match the declared mime")
	}

	return s.repo.MarkReady(ctx, t.UserID, mediaID, mediaUploadedEntry)
}

// ResolveDownloadURL returns a short-lived presigned GET URL for a ready blob.
// The HTTP layer 302-redirects the client to it.
func (s *Service) ResolveDownloadURL(ctx context.Context, t Tenant, mediaID string) (string, error) {
	m, err := s.repo.GetByID(ctx, t.UserID, mediaID)
	if err != nil {
		return "", err
	}
	if m.Status != StatusReady {
		return "", platformerrors.NotFound("media not available")
	}
	url, err := s.blob.PresignGet(ctx, s.bucket, m.S3Key, downloadTTL)
	if err != nil {
		return "", platformerrors.Unavailable("object store unavailable", err)
	}
	return url, nil
}

// Delete removes the caller's media row and its blobs; blob deletion is best-effort,
// since an orphaned object is cheaper than a failed delete.
func (s *Service) Delete(ctx context.Context, t Tenant, mediaID string) error {
	res, err := s.repo.Delete(ctx, t.UserID, mediaID)
	if err != nil {
		return err
	}
	for _, key := range append([]string{res.S3Key}, res.VariantKeys...) {
		if err := s.blob.Delete(ctx, s.bucket, key); err != nil {
			s.log.Warn("media: blob delete failed", zap.String("key", key), zap.Error(err))
		}
	}
	return nil
}

// ListChecksums returns the caller's ready media at or above sinceUSN.
func (s *Service) ListChecksums(ctx context.Context, t Tenant, sinceUSN int64) ([]Checksum, error) {
	return s.repo.ListChecksums(ctx, t.UserID, sinceUSN)
}
