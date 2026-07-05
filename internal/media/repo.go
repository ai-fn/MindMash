package media

import (
	"context"
	"encoding/hex"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"

	platformdb "github.com/mindmesh/mindmesh/internal/platform/db"
	platformerrors "github.com/mindmesh/mindmesh/internal/platform/errors"
	"github.com/mindmesh/mindmesh/internal/platform/events"
)

const uniqueViolation = "23505"

// mediaColumns is the canonical select list, kept next to the scan helper.
const mediaColumns = "id, user_id, profile_id, checksum, kind, mime, s3_key, bytes, status, usn, mtime, created_at"

// Variant is a derived rendition row.
type Variant struct {
	UserID  string
	MediaID string
	Variant string
	S3Key   string
	Mime    string
	Width   int
	Height  int
}

// Checksum is a single entry returned by ListChecksums.
type Checksum struct {
	MediaID  string
	Checksum []byte
	USN      int64
	Status   string
}

// DeleteResult reports what Delete removed and whether the shared blob is now
// unreferenced and safe to garbage collect.
type DeleteResult struct {
	S3Key       string
	VariantKeys []string
	Orphaned    bool // true when no user references the checksum any more
}

// Repo is the Postgres persistence layer for Media. Every query is scoped by
// user_id (the Citus shard key); the one deliberate exception is the cross-user
// reference count on delete, which must scan all shards.
type Repo struct {
	pool *pgxpool.Pool
}

// NewRepo returns a Repo backed by pool.
func NewRepo(pool *pgxpool.Pool) *Repo { return &Repo{pool: pool} }

// nextUSN atomically bumps and returns the caller's monotonic USN counter.
func nextUSN(ctx context.Context, tx pgx.Tx, userID string) (int64, error) {
	var usn int64
	err := tx.QueryRow(ctx, `
		INSERT INTO user_usn (user_id, usn) VALUES ($1, 1)
		ON CONFLICT (user_id) DO UPDATE SET usn = user_usn.usn + 1
		RETURNING usn`, userID).Scan(&usn)
	if err != nil {
		return 0, fmt.Errorf("media: next usn: %w", err)
	}
	return usn, nil
}

// InsertPending records a presigned upload as a pending row (usn 0 until
// confirmed). Returns AlreadyExists when the (user_id, checksum) row exists.
func (r *Repo) InsertPending(ctx context.Context, m *Media) error {
	_, err := r.pool.Exec(ctx, `
		INSERT INTO media (`+mediaColumns+`)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, now(), now())`,
		m.ID, m.UserID, m.ProfileID, m.Checksum, m.Kind, m.Mime, m.S3Key, m.Bytes, StatusPending, int64(0))
	if err != nil {
		var pgErr *pgconn.PgError
		if errors.As(err, &pgErr) && pgErr.Code == uniqueViolation {
			return platformerrors.AlreadyExists("media already exists for this checksum")
		}
		return fmt.Errorf("media: insert pending: %w", err)
	}
	return nil
}

// InsertReadyLinked writes a ready row that reuses a blob another (or this) user
// already uploaded — the dedupe short-circuit. It assigns a USN and emits
// media.uploaded atomically. outboxEntry builds the event from the stored row.
func (r *Repo) InsertReadyLinked(ctx context.Context, m *Media, outboxEntry func(*Media) events.OutboxEntry) (*Media, error) {
	err := platformdb.WithTx(ctx, r.pool, func(tx pgx.Tx) error {
		usn, err := nextUSN(ctx, tx, m.UserID)
		if err != nil {
			return err
		}
		m.USN = usn
		m.Status = StatusReady
		now := time.Now().UTC()
		m.Mtime, m.CreatedAt = now, now
		_, err = tx.Exec(ctx, `
			INSERT INTO media (`+mediaColumns+`)
			VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $11)`,
			m.ID, m.UserID, m.ProfileID, m.Checksum, m.Kind, m.Mime, m.S3Key, m.Bytes, StatusReady, usn, now)
		if err != nil {
			var pgErr *pgconn.PgError
			if errors.As(err, &pgErr) && pgErr.Code == uniqueViolation {
				return platformerrors.AlreadyExists("media already exists for this checksum")
			}
			return fmt.Errorf("media: insert ready: %w", err)
		}
		return events.WriteOutbox(ctx, tx, []events.OutboxEntry{outboxEntry(m)})
	})
	if err != nil {
		return nil, err
	}
	return m, nil
}

// MarkReady transitions a pending row to ready, assigns a USN, and emits
// media.uploaded — all atomically. Returns NotFound when there is no pending row.
func (r *Repo) MarkReady(ctx context.Context, userID, id string, outboxEntry func(*Media) events.OutboxEntry) (*Media, error) {
	var out *Media
	err := platformdb.WithTx(ctx, r.pool, func(tx pgx.Tx) error {
		usn, err := nextUSN(ctx, tx, userID)
		if err != nil {
			return err
		}
		row := tx.QueryRow(ctx, `
			UPDATE media SET status = $3, usn = $4, mtime = now()
			WHERE user_id = $1 AND id = $2 AND status = $5
			RETURNING `+mediaColumns,
			userID, id, StatusReady, usn, StatusPending)
		m, err := scanMedia(row)
		if err != nil {
			if errors.Is(err, pgx.ErrNoRows) {
				return platformerrors.NotFound("no pending media to confirm")
			}
			return fmt.Errorf("media: mark ready: %w", err)
		}
		if err := events.WriteOutbox(ctx, tx, []events.OutboxEntry{outboxEntry(m)}); err != nil {
			return err
		}
		out = m
		return nil
	})
	if err != nil {
		return nil, err
	}
	return out, nil
}

// GetByID loads one media row scoped to the caller. Returns NotFound if absent.
func (r *Repo) GetByID(ctx context.Context, userID, id string) (*Media, error) {
	row := r.pool.QueryRow(ctx, `SELECT `+mediaColumns+` FROM media WHERE user_id = $1 AND id = $2`, userID, id)
	m, err := scanMedia(row)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, platformerrors.NotFound("media not found")
		}
		return nil, fmt.Errorf("media: get by id: %w", err)
	}
	return m, nil
}

// FindByChecksum returns the caller's ready row for a checksum, or (nil, nil)
// when none exists.
func (r *Repo) FindByChecksum(ctx context.Context, userID string, checksum []byte) (*Media, error) {
	row := r.pool.QueryRow(ctx,
		`SELECT `+mediaColumns+` FROM media WHERE user_id = $1 AND checksum = $2`, userID, checksum)
	m, err := scanMedia(row)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, nil
		}
		return nil, fmt.Errorf("media: find by checksum: %w", err)
	}
	return m, nil
}

// InsertVariant records a derived rendition. Idempotent: a re-processed event is
// a no-op rather than an error.
func (r *Repo) InsertVariant(ctx context.Context, v Variant) error {
	_, err := r.pool.Exec(ctx, `
		INSERT INTO media_variants (user_id, media_id, variant, s3_key, mime, width, height)
		VALUES ($1, $2, $3, $4, $5, $6, $7)
		ON CONFLICT (user_id, media_id, variant) DO NOTHING`,
		v.UserID, v.MediaID, v.Variant, v.S3Key, v.Mime, v.Width, v.Height)
	if err != nil {
		return fmt.Errorf("media: insert variant: %w", err)
	}
	return nil
}

// Delete removes the caller's media row and its variants, then reports whether
// the shared blob is now unreferenced (so the caller can GC S3). The remaining
// count is a cross-shard scan — acceptable on the rare delete path.
func (r *Repo) Delete(ctx context.Context, userID, id string) (DeleteResult, error) {
	var res DeleteResult
	err := platformdb.WithTx(ctx, r.pool, func(tx pgx.Tx) error {
		var s3Key string
		var checksum []byte
		err := tx.QueryRow(ctx,
			`SELECT s3_key, checksum FROM media WHERE user_id = $1 AND id = $2`, userID, id).
			Scan(&s3Key, &checksum)
		if err != nil {
			if errors.Is(err, pgx.ErrNoRows) {
				return platformerrors.NotFound("media not found")
			}
			return fmt.Errorf("media: delete lookup: %w", err)
		}

		rows, err := tx.Query(ctx,
			`DELETE FROM media_variants WHERE user_id = $1 AND media_id = $2 RETURNING s3_key`, userID, id)
		if err != nil {
			return fmt.Errorf("media: delete variants: %w", err)
		}
		variantKeys, err := pgx.CollectRows(rows, pgx.RowTo[string])
		if err != nil {
			return fmt.Errorf("media: scan variant keys: %w", err)
		}

		if _, err := tx.Exec(ctx, `DELETE FROM media WHERE user_id = $1 AND id = $2`, userID, id); err != nil {
			return fmt.Errorf("media: delete row: %w", err)
		}

		var remaining int64
		if err := tx.QueryRow(ctx,
			`SELECT count(*) FROM media WHERE checksum = $1`, checksum).Scan(&remaining); err != nil {
			return fmt.Errorf("media: count refs: %w", err)
		}

		res = DeleteResult{S3Key: s3Key, VariantKeys: variantKeys, Orphaned: remaining == 0}
		return nil
	})
	if err != nil {
		return DeleteResult{}, err
	}
	return res, nil
}

// ListChecksums returns the caller's ready media at or above sinceUSN, ordered
// by USN. Pending (unconfirmed) rows are excluded — their blobs aren't fetchable.
func (r *Repo) ListChecksums(ctx context.Context, userID string, sinceUSN int64) ([]Checksum, error) {
	rows, err := r.pool.Query(ctx, `
		SELECT id, checksum, usn, status FROM media
		WHERE user_id = $1 AND status = $2 AND usn >= $3
		ORDER BY usn`, userID, StatusReady, sinceUSN)
	if err != nil {
		return nil, fmt.Errorf("media: list checksums: %w", err)
	}
	return pgx.CollectRows(rows, func(row pgx.CollectableRow) (Checksum, error) {
		var c Checksum
		return c, row.Scan(&c.MediaID, &c.Checksum, &c.USN, &c.Status)
	})
}

// scanMedia scans one media row from the canonical column order.
func scanMedia(row pgx.Row) (*Media, error) {
	var m Media
	if err := row.Scan(&m.ID, &m.UserID, &m.ProfileID, &m.Checksum, &m.Kind, &m.Mime,
		&m.S3Key, &m.Bytes, &m.Status, &m.USN, &m.Mtime, &m.CreatedAt); err != nil {
		return nil, err
	}
	return &m, nil
}

// checksumHex is a small helper for logging/keys outside the domain type.
func checksumHex(sum []byte) string { return hex.EncodeToString(sum) }
