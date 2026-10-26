package scheduler

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/mindmesh/mindmesh/internal/platform/apperr"
	platformdb "github.com/mindmesh/mindmesh/internal/platform/db"
	"github.com/mindmesh/mindmesh/internal/platform/events"
	"github.com/mindmesh/mindmesh/internal/platform/usn"
)

// Column lists kept next to the scans that consume them so the order stays in
// sync with internal/collection/repo.go, which owns these tables' schema.
const (
	cardColumns       = "id, user_id, profile_id, deck_id, state, queue, due, stability, difficulty, reps, lapses, last_review, usn, mtime"
	deckColumns       = "id, path::text, config_id"
	deckConfigColumns = "new_per_day, rev_per_day, desired_retention, fsrs_weights, rollover_hour, timezone, max_interval_days"
)

// dbtx is satisfied by both *pgxpool.Pool and pgx.Tx, allowing the same query
// methods to work inside or outside a transaction without duplication.
type dbtx interface {
	Exec(ctx context.Context, sql string, arguments ...any) (pgconn.CommandTag, error)
	Query(ctx context.Context, sql string, args ...any) (pgx.Rows, error)
	QueryRow(ctx context.Context, sql string, args ...any) pgx.Row
}

// rowScanner is the common Scan surface of pgx.Row and pgx.Rows.
type rowScanner interface {
	Scan(dest ...any) error
}

// PostgresRepository shares Collection's tables and owns review_logs.
type PostgresRepository struct {
	pool *pgxpool.Pool
	db   dbtx
}

// NewPostgresRepository returns a repository that uses the given pool.
func NewPostgresRepository(pool *pgxpool.Pool) *PostgresRepository {
	return &PostgresRepository{pool: pool, db: pool}
}

func (r *PostgresRepository) withDB(db dbtx) *PostgresRepository {
	return &PostgresRepository{pool: r.pool, db: db}
}

// WithTx runs fn inside a database transaction. The Repository passed to fn
// uses the transaction connection; the outer repository is unchanged.
func (r *PostgresRepository) WithTx(ctx context.Context, fn func(Repository) error) error {
	return platformdb.WithTx(ctx, r.pool, pgx.TxOptions{}, func(tx pgx.Tx) error {
		return fn(r.withDB(tx))
	})
}

func mapNoRows(err error, msg string) error {
	if errors.Is(err, pgx.ErrNoRows) {
		return apperr.NotFound(msg)
	}
	return err
}

// ─── NextUSN ─────────────────────────────────────────────────────────────────

func (r *PostgresRepository) NextUSN(ctx context.Context, userID string) (int64, error) {
	return usn.Next(ctx, r.db, userID)
}

// ─── Card ────────────────────────────────────────────────────────────────────

func scanCard(s rowScanner) (*Card, error) {
	c := &Card{}
	var state, queue int16
	if err := s.Scan(&c.ID, &c.UserID, &c.ProfileID, &c.DeckID, &state, &queue,
		&c.Due, &c.Stability, &c.Difficulty, &c.Reps, &c.Lapses, &c.LastReview, &c.USN, &c.Mtime); err != nil {
		return nil, err
	}
	c.State = CardState(state)
	c.Queue = queue
	return c, nil
}

func collectCards(rows pgx.Rows, op string) ([]Card, error) {
	defer rows.Close()
	var cards []Card
	for rows.Next() {
		c, err := scanCard(rows)
		if err != nil {
			return nil, fmt.Errorf("scheduler: %s scan: %w", op, err)
		}
		cards = append(cards, *c)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("scheduler: %s rows: %w", op, err)
	}
	return cards, nil
}

func (r *PostgresRepository) GetCard(ctx context.Context, userID, cardID string) (*Card, error) {
	c, err := scanCard(r.db.QueryRow(ctx,
		`SELECT `+cardColumns+` FROM cards WHERE user_id = $1 AND id = $2`, userID, cardID))
	if err != nil {
		return nil, fmt.Errorf("scheduler: get card: %w", mapNoRows(err, "card not found"))
	}
	return c, nil
}

// LoadCardForUpdate locks the card row for the duration of the answer
// transaction; must be called inside WithTx.
func (r *PostgresRepository) LoadCardForUpdate(ctx context.Context, userID, cardID string) (*Card, error) {
	c, err := scanCard(r.db.QueryRow(ctx,
		`SELECT `+cardColumns+` FROM cards WHERE user_id = $1 AND id = $2 FOR UPDATE`, userID, cardID))
	if err != nil {
		return nil, fmt.Errorf("scheduler: load card for update: %w", mapNoRows(err, "card not found"))
	}
	return c, nil
}

// UpdateCardSchedule writes the scheduling columns back to `cards`, bumping
// usn/mtime. Structural columns (note_id, deck_id, template_ord) are untouched.
func (r *PostgresRepository) UpdateCardSchedule(ctx context.Context, c *Card) error {
	ct, err := r.db.Exec(ctx, `
		UPDATE cards SET state = $3, queue = $4, due = $5, stability = $6, difficulty = $7,
			reps = $8, lapses = $9, last_review = $10, usn = $11, mtime = $12
		WHERE user_id = $1 AND id = $2`,
		c.UserID, c.ID, int16(c.State), c.Queue, c.Due, c.Stability, c.Difficulty,
		c.Reps, c.Lapses, c.LastReview, c.USN, c.Mtime)
	if err != nil {
		return fmt.Errorf("scheduler: update card schedule: %w", err)
	}
	if ct.RowsAffected() == 0 {
		return apperr.NotFound("card not found")
	}
	return nil
}

// ListDueCards returns cards in deckIDs with one of states due at or before now,
// ordered by due ascending. Uses the cards_due partial index (queue >= 0 AND state <> 0).
func (r *PostgresRepository) ListDueCards(ctx context.Context, userID string, deckIDs []string, states []CardState, now time.Time, limit int) ([]Card, error) {
	st := make([]int16, len(states))
	for i, s := range states {
		st[i] = int16(s)
	}
	rows, err := r.db.Query(ctx,
		`SELECT `+cardColumns+` FROM cards
		 WHERE user_id = $1 AND deck_id = ANY($2::uuid[]) AND queue >= 0 AND state <> 0
		   AND state = ANY($3::smallint[]) AND due <= $4
		 ORDER BY due ASC LIMIT $5`,
		userID, deckIDs, st, now, limit)
	if err != nil {
		return nil, fmt.Errorf("scheduler: list due cards: %w", err)
	}
	return collectCards(rows, "list due cards")
}

// ListNewCards returns unsuspended, unburied new cards in deckIDs ordered by id
// (ULID, time-sortable) ascending. Uses the cards_new partial index.
func (r *PostgresRepository) ListNewCards(ctx context.Context, userID string, deckIDs []string, limit int) ([]Card, error) {
	rows, err := r.db.Query(ctx,
		`SELECT `+cardColumns+` FROM cards
		 WHERE user_id = $1 AND deck_id = ANY($2::uuid[]) AND state = 0 AND queue >= 0
		 ORDER BY id ASC LIMIT $3`,
		userID, deckIDs, limit)
	if err != nil {
		return nil, fmt.Errorf("scheduler: list new cards: %w", err)
	}
	return collectCards(rows, "list new cards")
}

// CountAvailable splits into two scans so each uses its partial index (cards_new, cards_due).
func (r *PostgresRepository) CountAvailable(ctx context.Context, userID string, deckIDs []string, now time.Time) (Counts, error) {
	var counts Counts
	err := r.db.QueryRow(ctx, `
		SELECT n.new, d.learning, d.review
		FROM (SELECT COUNT(*) AS new FROM cards
		      WHERE user_id = $1 AND deck_id = ANY($2::uuid[]) AND state = 0 AND queue >= 0) n,
		     (SELECT COUNT(*) FILTER (WHERE state IN (1, 3)) AS learning,
		             COUNT(*) FILTER (WHERE state = 2) AS review
		      FROM cards
		      WHERE user_id = $1 AND deck_id = ANY($2::uuid[]) AND queue >= 0 AND state <> 0 AND due <= $3) d`,
		userID, deckIDs, now).Scan(&counts.New, &counts.Learning, &counts.Review)
	if err != nil {
		return Counts{}, fmt.Errorf("scheduler: count available: %w", err)
	}
	return counts, nil
}

// ListBuriedCardIDs reads the bury time from mtime, so any later write to a buried
// card keeps it buried until the next day. Locks in id order so callers can take user_usn after.
func (r *PostgresRepository) ListBuriedCardIDs(ctx context.Context, userID string, deckIDs []string, before time.Time) ([]string, error) {
	rows, err := r.db.Query(ctx,
		`SELECT id FROM cards WHERE user_id = $1 AND deck_id = ANY($2::uuid[]) AND queue = -2 AND mtime < $3
		 ORDER BY id FOR UPDATE`,
		userID, deckIDs, before)
	if err != nil {
		return nil, fmt.Errorf("scheduler: list buried cards: %w", err)
	}
	ids, err := pgx.CollectRows(rows, pgx.RowTo[string])
	if err != nil {
		return nil, fmt.Errorf("scheduler: list buried cards: %w", err)
	}
	return ids, nil
}

func (r *PostgresRepository) UnburyCards(ctx context.Context, userID string, cardIDs []string, usn int64) ([]Card, error) {
	rows, err := r.db.Query(ctx,
		`UPDATE cards SET queue = 0, usn = $3, mtime = now()
		 WHERE user_id = $1 AND id = ANY($2::uuid[]) AND queue = -2
		 RETURNING `+cardColumns,
		userID, cardIDs, usn)
	if err != nil {
		return nil, fmt.Errorf("scheduler: unbury cards: %w", err)
	}
	return collectCards(rows, "unbury cards")
}

// ─── Deck / DeckConfig ───────────────────────────────────────────────────────

func scanDeck(s rowScanner) (*Deck, error) {
	d := &Deck{}
	if err := s.Scan(&d.ID, &d.Path, &d.ConfigID); err != nil {
		return nil, err
	}
	return d, nil
}

func (r *PostgresRepository) GetDeck(ctx context.Context, userID, deckID string) (*Deck, error) {
	d, err := scanDeck(r.db.QueryRow(ctx,
		`SELECT `+deckColumns+` FROM decks WHERE user_id = $1 AND id = $2`, userID, deckID))
	if err != nil {
		return nil, fmt.Errorf("scheduler: get deck: %w", mapNoRows(err, "deck not found"))
	}
	return d, nil
}

func (r *PostgresRepository) ListSubtreeDecks(ctx context.Context, userID, rootPath string) ([]Deck, error) {
	rows, err := r.db.Query(ctx,
		`SELECT `+deckColumns+` FROM decks WHERE user_id = $1 AND path <@ $2::ltree ORDER BY path ASC`,
		userID, rootPath)
	if err != nil {
		return nil, fmt.Errorf("scheduler: list subtree decks: %w", err)
	}
	defer rows.Close()

	var items []Deck
	for rows.Next() {
		d, err := scanDeck(rows)
		if err != nil {
			return nil, fmt.Errorf("scheduler: list subtree decks scan: %w", err)
		}
		items = append(items, *d)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("scheduler: list subtree decks rows: %w", err)
	}
	return items, nil
}

// GetEffectiveDeckConfig resolves the deck's config_id to a DeckConfig,
// falling back to DefaultDeckConfig when the deck has no config assigned.
func (r *PostgresRepository) GetEffectiveDeckConfig(ctx context.Context, userID, deckID string) (*DeckConfig, error) {
	deck, err := r.GetDeck(ctx, userID, deckID)
	if err != nil {
		return nil, err
	}
	if deck.ConfigID == nil {
		return DefaultDeckConfig(), nil
	}

	dc := &DeckConfig{ID: *deck.ConfigID}
	var rollover int16
	err = r.db.QueryRow(ctx,
		`SELECT `+deckConfigColumns+` FROM deck_configs WHERE user_id = $1 AND id = $2`,
		userID, *deck.ConfigID).Scan(
		&dc.NewPerDay, &dc.RevPerDay, &dc.DesiredRetention, &dc.FSRSWeights, &rollover, &dc.Timezone, &dc.MaxIntervalDays)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return DefaultDeckConfig(), nil
		}
		return nil, fmt.Errorf("scheduler: get effective deck config: %w", err)
	}
	dc.RolloverHour = int32(rollover)
	return dc, nil
}

// CountReviewedToday returns how many answers since dayStart were given in deckIDs,
// split new/learning/review by the card's state at answer time (prev_state).
func (r *PostgresRepository) CountReviewedToday(ctx context.Context, userID string, deckIDs []string, dayStart time.Time) (Counts, error) {
	var counts Counts
	err := r.db.QueryRow(ctx, `
		SELECT
			COUNT(*) FILTER (WHERE prev_state = 0),
			COUNT(*) FILTER (WHERE prev_state IN (1, 3)),
			COUNT(*) FILTER (WHERE prev_state = 2)
		FROM review_logs
		WHERE user_id = $1 AND deck_id = ANY($2::uuid[]) AND ts >= $3`,
		userID, deckIDs, dayStart).Scan(&counts.New, &counts.Learning, &counts.Review)
	if err != nil {
		return Counts{}, fmt.Errorf("scheduler: count reviewed today: %w", err)
	}
	return counts, nil
}

// ─── ReviewLog ───────────────────────────────────────────────────────────────

func (r *PostgresRepository) InsertReviewLog(ctx context.Context, rl *ReviewLog) error {
	_, err := r.db.Exec(ctx, `
		INSERT INTO review_logs (id, user_id, profile_id, card_id, deck_id, grade, prev_state, prev_ivl, new_ivl, time_ms, ts, client_review_id)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, NULLIF($12, '')::uuid)`,
		rl.ID, rl.UserID, rl.ProfileID, rl.CardID, rl.DeckID, int16(rl.Grade), int16(rl.PrevState),
		rl.PrevIvl, rl.NewIvl, rl.TimeMs, rl.Ts, rl.ClientReviewID)
	if err != nil {
		// Reached only when the same key is used concurrently on two cards; the card lock serializes same-card retries.
		// Prefix match: Citus suffixes shard ids to index names.
		var pgErr *pgconn.PgError
		if errors.As(err, &pgErr) && pgErr.Code == "23505" && strings.HasPrefix(pgErr.ConstraintName, "rl_client_review") {
			return apperr.InvalidArgument("client_review_id was already used for another card")
		}
		return fmt.Errorf("scheduler: insert review log: %w", err)
	}
	return nil
}

func (r *PostgresRepository) ReviewedCardID(ctx context.Context, userID, clientReviewID string) (string, bool, error) {
	var cardID string
	err := r.db.QueryRow(ctx,
		`SELECT card_id FROM review_logs WHERE user_id = $1 AND client_review_id = $2`,
		userID, clientReviewID).Scan(&cardID)
	if errors.Is(err, pgx.ErrNoRows) {
		return "", false, nil
	}
	if err != nil {
		return "", false, fmt.Errorf("scheduler: find review by client id: %w", err)
	}
	return cardID, true, nil
}

// ─── Outbox ──────────────────────────────────────────────────────────────────

func (r *PostgresRepository) WriteOutbox(ctx context.Context, entries []events.OutboxEntry) error {
	tx, ok := r.db.(pgx.Tx)
	if !ok {
		panic("scheduler: WriteOutbox called outside a WithTx transaction")
	}
	return events.WriteOutbox(ctx, tx, entries)
}
