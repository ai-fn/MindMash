package sync

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
)

// Column lists kept next to the scans that consume them so the order stays in
// sync with internal/collection/repo.go, which owns these tables' schema.
const (
	noteTypeColumns     = "id, name, fields, card_templates, css, usn, mtime"
	noteColumns         = "id, note_type_id, fields, tags, flags, usn, mtime"
	cardColumns         = "id, note_id, deck_id, template_ord, state, queue, due, stability, difficulty, reps, lapses, last_review, usn, mtime"
	deckColumns         = "id, parent_id, name, path::text, config_id, usn, mtime"
	deckConfigColumns   = "id, name, new_per_day, rev_per_day, desired_retention, fsrs_weights, learning_steps, rollover_hour, timezone, usn, mtime"
	filteredDeckColumns = "id, name, query, usn, mtime"
	tagColumns          = "id, name, usn, mtime"
)

// dbtx is the subset of *pgxpool.Pool that Sync needs. Most methods here are
// reads against Collection/Scheduler's colocated tables; Exec backs the
// sync_applied_ops writes Sync issues for its own push-path idempotency
// bookkeeping (see GetAppliedOp/RecordAppliedOp below).
type dbtx interface {
	Query(ctx context.Context, sql string, args ...any) (pgx.Rows, error)
	QueryRow(ctx context.Context, sql string, args ...any) pgx.Row
	Exec(ctx context.Context, sql string, arguments ...any) (pgconn.CommandTag, error)
}

// PostgresRepository implements Repository over the shared Collection
// Postgres pool (same DB, same tables; owns none of them — read-only).
type PostgresRepository struct {
	db dbtx
}

// NewPostgresRepository returns a repository that uses the given pool.
func NewPostgresRepository(pool *pgxpool.Pool) *PostgresRepository {
	return &PostgresRepository{db: pool}
}

// ─── Keyset pagination ───────────────────────────────────────────────────────
// $1 is user_id, $2 the limit, $3 the usn ceiling; the cursor fills $4 onward.

func afterEntity(c Cursor) string {
	if c.ID == "" {
		return "usn > $4"
	}
	return "(usn, id) > ($4, $5::uuid)"
}

func entityArgs(userID string, limit int, upTo int64, c Cursor) []any {
	if c.ID == "" {
		return []any{userID, limit, upTo, c.USN}
	}
	return []any{userID, limit, upTo, c.USN, c.ID}
}

func afterTombstone(c Cursor) string {
	if c.ID == "" {
		return "usn > $4"
	}
	return "(usn, type, id) > ($4, $5, $6::uuid)"
}

func tombstoneArgs(userID string, limit int, upTo int64, c Cursor) []any {
	if c.ID == "" {
		return []any{userID, limit, upTo, c.USN}
	}
	return []any{userID, limit, upTo, c.USN, c.Type, c.ID}
}

// ─── LatestUSN ───────────────────────────────────────────────────────────────

func (r *PostgresRepository) LatestUSN(ctx context.Context, userID string) (int64, error) {
	var usn int64
	err := r.db.QueryRow(ctx, `SELECT usn FROM user_usn WHERE user_id = $1`, userID).Scan(&usn)
	if err != nil {
		if err == pgx.ErrNoRows {
			return 0, nil
		}
		return 0, fmt.Errorf("sync: latest usn: %w", err)
	}
	return usn, nil
}

// ─── NoteType ────────────────────────────────────────────────────────────────

func (r *PostgresRepository) ListNoteTypesSince(ctx context.Context, userID string, after Cursor, upTo int64, limit int) ([]NoteType, error) {
	rows, err := r.db.Query(ctx,
		`SELECT `+noteTypeColumns+` FROM note_types WHERE user_id = $1 AND usn <= $3 AND `+afterEntity(after)+` ORDER BY usn, id LIMIT $2`,
		entityArgs(userID, limit, upTo, after)...)
	if err != nil {
		return nil, fmt.Errorf("sync: list note_types since: %w", err)
	}
	defer rows.Close()

	var items []NoteType
	for rows.Next() {
		var nt NoteType
		var fields, templates []byte
		if err := rows.Scan(&nt.ID, &nt.Name, &fields, &templates, &nt.CSS, &nt.USN, &nt.Mtime); err != nil {
			return nil, fmt.Errorf("sync: scan note_type: %w", err)
		}
		if err := json.Unmarshal(fields, &nt.Fields); err != nil {
			return nil, fmt.Errorf("sync: decode note_type fields: %w", err)
		}
		if err := json.Unmarshal(templates, &nt.CardTemplates); err != nil {
			return nil, fmt.Errorf("sync: decode note_type templates: %w", err)
		}
		items = append(items, nt)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("sync: list note_types rows: %w", err)
	}
	return items, nil
}

// ─── Note ────────────────────────────────────────────────────────────────────

func (r *PostgresRepository) ListNotesSince(ctx context.Context, userID string, after Cursor, upTo int64, limit int) ([]Note, error) {
	rows, err := r.db.Query(ctx,
		`SELECT `+noteColumns+` FROM notes WHERE user_id = $1 AND usn <= $3 AND `+afterEntity(after)+` ORDER BY usn, id LIMIT $2`,
		entityArgs(userID, limit, upTo, after)...)
	if err != nil {
		return nil, fmt.Errorf("sync: list notes since: %w", err)
	}
	defer rows.Close()

	var items []Note
	for rows.Next() {
		var n Note
		var flags int16
		if err := rows.Scan(&n.ID, &n.NoteTypeID, &n.Fields, &n.Tags, &flags, &n.USN, &n.Mtime); err != nil {
			return nil, fmt.Errorf("sync: scan note: %w", err)
		}
		n.Flags = int32(flags)
		items = append(items, n)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("sync: list notes rows: %w", err)
	}
	return items, nil
}

// ─── Card ────────────────────────────────────────────────────────────────────

func (r *PostgresRepository) ListCardsSince(ctx context.Context, userID string, after Cursor, upTo int64, limit int) ([]Card, error) {
	rows, err := r.db.Query(ctx,
		`SELECT `+cardColumns+` FROM cards WHERE user_id = $1 AND usn <= $3 AND `+afterEntity(after)+` ORDER BY usn, id LIMIT $2`,
		entityArgs(userID, limit, upTo, after)...)
	if err != nil {
		return nil, fmt.Errorf("sync: list cards since: %w", err)
	}
	defer rows.Close()

	var items []Card
	for rows.Next() {
		var c Card
		var templateOrd, state, queue, reps, lapses int
		if err := rows.Scan(&c.ID, &c.NoteID, &c.DeckID, &templateOrd, &state, &queue,
			&c.Due, &c.Stability, &c.Difficulty, &reps, &lapses, &c.LastReview, &c.USN, &c.Mtime); err != nil {
			return nil, fmt.Errorf("sync: scan card: %w", err)
		}
		c.TemplateOrd, c.State, c.Queue, c.Reps, c.Lapses = templateOrd, state, queue, reps, lapses
		items = append(items, c)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("sync: list cards rows: %w", err)
	}
	return items, nil
}

// ─── Deck ────────────────────────────────────────────────────────────────────

func (r *PostgresRepository) ListDecksSince(ctx context.Context, userID string, after Cursor, upTo int64, limit int) ([]Deck, error) {
	rows, err := r.db.Query(ctx,
		`SELECT `+deckColumns+` FROM decks WHERE user_id = $1 AND usn <= $3 AND `+afterEntity(after)+` ORDER BY usn, id LIMIT $2`,
		entityArgs(userID, limit, upTo, after)...)
	if err != nil {
		return nil, fmt.Errorf("sync: list decks since: %w", err)
	}
	defer rows.Close()

	var items []Deck
	for rows.Next() {
		var d Deck
		if err := rows.Scan(&d.ID, &d.ParentID, &d.Name, &d.Path, &d.ConfigID, &d.USN, &d.Mtime); err != nil {
			return nil, fmt.Errorf("sync: scan deck: %w", err)
		}
		items = append(items, d)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("sync: list decks rows: %w", err)
	}
	return items, nil
}

// ─── DeckConfig ──────────────────────────────────────────────────────────────

func (r *PostgresRepository) ListDeckConfigsSince(ctx context.Context, userID string, after Cursor, upTo int64, limit int) ([]DeckConfig, error) {
	rows, err := r.db.Query(ctx,
		`SELECT `+deckConfigColumns+` FROM deck_configs WHERE user_id = $1 AND usn <= $3 AND `+afterEntity(after)+` ORDER BY usn, id LIMIT $2`,
		entityArgs(userID, limit, upTo, after)...)
	if err != nil {
		return nil, fmt.Errorf("sync: list deck_configs since: %w", err)
	}
	defer rows.Close()

	var items []DeckConfig
	for rows.Next() {
		var dc DeckConfig
		var rollover int16
		if err := rows.Scan(&dc.ID, &dc.Name, &dc.NewPerDay, &dc.RevPerDay, &dc.DesiredRetention,
			&dc.FSRSWeights, &dc.LearningSteps, &rollover, &dc.Timezone, &dc.USN, &dc.Mtime); err != nil {
			return nil, fmt.Errorf("sync: scan deck_config: %w", err)
		}
		dc.RolloverHour = int32(rollover)
		items = append(items, dc)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("sync: list deck_configs rows: %w", err)
	}
	return items, nil
}

// ─── FilteredDeck ────────────────────────────────────────────────────────────

func (r *PostgresRepository) ListFilteredDecksSince(ctx context.Context, userID string, after Cursor, upTo int64, limit int) ([]FilteredDeck, error) {
	rows, err := r.db.Query(ctx,
		`SELECT `+filteredDeckColumns+` FROM filtered_decks WHERE user_id = $1 AND usn <= $3 AND `+afterEntity(after)+` ORDER BY usn, id LIMIT $2`,
		entityArgs(userID, limit, upTo, after)...)
	if err != nil {
		return nil, fmt.Errorf("sync: list filtered_decks since: %w", err)
	}
	defer rows.Close()

	var items []FilteredDeck
	for rows.Next() {
		var fd FilteredDeck
		if err := rows.Scan(&fd.ID, &fd.Name, &fd.Query, &fd.USN, &fd.Mtime); err != nil {
			return nil, fmt.Errorf("sync: scan filtered_deck: %w", err)
		}
		items = append(items, fd)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("sync: list filtered_decks rows: %w", err)
	}
	return items, nil
}

// ─── Tag ─────────────────────────────────────────────────────────────────────

func (r *PostgresRepository) ListTagsSince(ctx context.Context, userID string, after Cursor, upTo int64, limit int) ([]Tag, error) {
	rows, err := r.db.Query(ctx,
		`SELECT `+tagColumns+` FROM tags WHERE user_id = $1 AND usn <= $3 AND `+afterEntity(after)+` ORDER BY usn, id LIMIT $2`,
		entityArgs(userID, limit, upTo, after)...)
	if err != nil {
		return nil, fmt.Errorf("sync: list tags since: %w", err)
	}
	defer rows.Close()

	var items []Tag
	for rows.Next() {
		var t Tag
		if err := rows.Scan(&t.ID, &t.Name, &t.USN, &t.Mtime); err != nil {
			return nil, fmt.Errorf("sync: scan tag: %w", err)
		}
		items = append(items, t)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("sync: list tags rows: %w", err)
	}
	return items, nil
}

// ─── Tombstone ───────────────────────────────────────────────────────────────

func (r *PostgresRepository) ListTombstonesSince(ctx context.Context, userID string, after Cursor, upTo int64, limit int) ([]Tombstone, error) {
	rows, err := r.db.Query(ctx,
		`SELECT type, id, usn, deleted_at FROM tombstones WHERE user_id = $1 AND usn <= $3 AND `+afterTombstone(after)+` ORDER BY usn, type, id LIMIT $2`,
		tombstoneArgs(userID, limit, upTo, after)...)
	if err != nil {
		return nil, fmt.Errorf("sync: list tombstones since: %w", err)
	}
	defer rows.Close()

	var items []Tombstone
	for rows.Next() {
		var t Tombstone
		if err := rows.Scan(&t.Type, &t.ID, &t.USN, &t.DeletedAt); err != nil {
			return nil, fmt.Errorf("sync: scan tombstone: %w", err)
		}
		items = append(items, t)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("sync: list tombstones rows: %w", err)
	}
	return items, nil
}

// ─── AppliedOp (push-path idempotency) ──────────────────────────────────────

func (r *PostgresRepository) GetAppliedOp(ctx context.Context, userID, opID string) (*AppliedOp, error) {
	var op AppliedOp
	op.OpID = opID
	err := r.db.QueryRow(ctx,
		`SELECT outcome, new_usn, applied_at FROM sync_applied_ops WHERE user_id = $1 AND op_id = $2`,
		userID, opID).Scan(&op.Outcome, &op.NewUSN, &op.AppliedAt)
	if err != nil {
		if err == pgx.ErrNoRows {
			return nil, nil
		}
		return nil, fmt.Errorf("sync: get applied op: %w", err)
	}
	return &op, nil
}

func (r *PostgresRepository) RecordAppliedOp(ctx context.Context, userID, opID string, outcome ApplyOutcome, newUSN int64) error {
	_, err := r.db.Exec(ctx,
		`INSERT INTO sync_applied_ops (user_id, op_id, outcome, new_usn) VALUES ($1, $2, $3, $4) ON CONFLICT (user_id, op_id) DO NOTHING`,
		userID, opID, outcome, newUSN)
	if err != nil {
		return fmt.Errorf("sync: record applied op: %w", err)
	}
	return nil
}
