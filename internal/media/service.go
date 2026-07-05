package media

import (
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
	PresignPut(ctx context.Context, bucket, key string, ttl time.Duration) (string, error)
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
	AlreadyExists bool
}

// Presign validates the request and returns a presigned upload URL — unless the
// blob is already stored (dedupe), in which case it records the caller's row and
// returns already_exists with no URL.
func (s *Service) Presign(ctx context.Context, t Tenant, filename, mime, sha256Hex string, bytes int64) (PresignResult, error) {
	req, err := ValidateUpload(mime, sha256Hex, bytes)
	if err != nil {
		return PresignResult{}, err
	}
	key := s3KeyFor(req.Checksum, req.Ext)

	// (1) The caller already has a row for this checksum.
	existing, err := s.repo.FindByChecksum(ctx, t.UserID, req.Checksum)
	if err != nil {
		return PresignResult{}, err
	}
	if existing != nil {
		if existing.Status == StatusReady {
			return PresignResult{MediaID: existing.ID, AlreadyExists: true}, nil
		}
		// A prior presign left a pending row; hand back a fresh upload URL so the
		// client can (re)PUT and then confirm.
		url, err := s.blob.PresignPut(ctx, s.bucket, existing.S3Key, presignTTL)
		if err != nil {
			return PresignResult{}, platformerrors.Unavailable("object store unavailable", err)
		}
		return PresignResult{MediaID: existing.ID, UploadURL: url}, nil
	}

	// (2) Another user already uploaded these exact bytes — the blob is shared.
	if _, found, err := s.blob.Stat(ctx, s.bucket, key); err != nil {
		return PresignResult{}, platformerrors.Unavailable("object store unavailable", err)
	} else if found {
		m := &Media{
			ID:        ids.NewUUID(),
			UserID:    t.UserID,
			ProfileID: t.ProfileID,
			Checksum:  req.Checksum,
			Kind:      req.Kind,
			Mime:      req.Mime,
			S3Key:     key,
			Bytes:     req.Bytes,
		}
		saved, err := s.repo.InsertReadyLinked(ctx, m, mediaUploadedEntry)
		if err != nil {
			// A concurrent request inserted this (user_id, checksum) first. Re-fetch
			// the winning row so we still return an idempotent dedupe result rather
			// than an error (saved is nil on this path).
			if platformerrors.IsAlreadyExists(err) {
				if ex, e := s.repo.FindByChecksum(ctx, t.UserID, req.Checksum); e == nil && ex != nil {
					return PresignResult{MediaID: ex.ID, AlreadyExists: true}, nil
				}
			}
			return PresignResult{}, err
		}
		return PresignResult{MediaID: saved.ID, AlreadyExists: true}, nil
	}

	// (3) New blob: record pending row and return an upload URL.
	m := &Media{
		ID:        ids.NewUUID(),
		UserID:    t.UserID,
		ProfileID: t.ProfileID,
		Checksum:  req.Checksum,
		Kind:      req.Kind,
		Mime:      req.Mime,
		S3Key:     key,
		Bytes:     req.Bytes,
	}
	if err := s.repo.InsertPending(ctx, m); err != nil {
		return PresignResult{}, err
	}
	url, err := s.blob.PresignPut(ctx, s.bucket, key, presignTTL)
	if err != nil {
		return PresignResult{}, platformerrors.Unavailable("object store unavailable", err)
	}
	return PresignResult{MediaID: m.ID, UploadURL: url}, nil
}

// Confirm verifies the client PUT landed and matches the declared size, then
// flips the row to ready and emits media.uploaded. It is idempotent.
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
	if !found {
		return nil, platformerrors.FailedPrecondition("upload not found; PUT the bytes before confirming")
	}
	if info.Size != m.Bytes {
		return nil, platformerrors.FailedPrecondition("uploaded size does not match the declared size")
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

// Delete unlinks the caller's media row and garbage-collects the shared blob
// once it is no longer referenced by any user. Blob deletion is best-effort:
// the metadata is already gone, and an orphaned object is cheaper than a failed
// user-facing delete.
func (s *Service) Delete(ctx context.Context, t Tenant, mediaID string) error {
	res, err := s.repo.Delete(ctx, t.UserID, mediaID)
	if err != nil {
		return err
	}
	if !res.Orphaned {
		return nil
	}
	for _, key := range append([]string{res.S3Key}, res.VariantKeys...) {
		if err := s.blob.Delete(ctx, s.bucket, key); err != nil {
			s.log.Warn("media: orphan blob GC failed", zap.String("key", key), zap.Error(err))
		}
	}
	return nil
}

// ListChecksums returns the caller's ready media at or above sinceUSN.
func (s *Service) ListChecksums(ctx context.Context, t Tenant, sinceUSN int64) ([]Checksum, error) {
	return s.repo.ListChecksums(ctx, t.UserID, sinceUSN)
}
