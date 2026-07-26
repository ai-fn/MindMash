package analytics

import (
	"context"
	"fmt"
	"time"

	"github.com/ClickHouse/clickhouse-go/v2/lib/driver"
)

// Tenant is the per-request scope. Every query filters on both fields; they are
// taken from the gateway-injected JWT context, never the request body.
type Tenant struct {
	UserID    string
	ProfileID string
}

// Repo runs aggregation queries against the ClickHouse review_events mirror.
// All reads use FINAL so the ReplacingMergeTree's duplicate rows (from
// at-least-once ingestion) are folded before aggregation.
type Repo struct {
	conn driver.Conn
}

// NewRepo wires the analytics read repo.
func NewRepo(conn driver.Conn) *Repo {
	return &Repo{conn: conn}
}

// Overview is the headline aggregate for a range.
type Overview struct {
	Reviews int64
	Correct int64
	TimeMs  int64
}

// Overview returns total reviews, correct count, and time spent since `since`.
func (r *Repo) Overview(ctx context.Context, t Tenant, since time.Time) (Overview, error) {
	const q = `SELECT toInt64(count()), toInt64(countIf(grade > 1)), toInt64(sum(time_ms))
		FROM review_events FINAL
		WHERE user_id = ? AND profile_id = ? AND ts >= ?`
	var o Overview
	row := r.conn.QueryRow(ctx, q, t.UserID, t.ProfileID, since)
	if err := row.Scan(&o.Reviews, &o.Correct, &o.TimeMs); err != nil {
		return Overview{}, fmt.Errorf("analytics: overview: %w", err)
	}
	return o, nil
}

// StudyDays returns the distinct UTC dates (descending) on which the tenant
// reviewed at least one card, used to compute the current study streak.
func (r *Repo) StudyDays(ctx context.Context, t Tenant) ([]time.Time, error) {
	const q = `SELECT DISTINCT toDate(ts) AS day
		FROM review_events FINAL
		WHERE user_id = ? AND profile_id = ?
		ORDER BY day DESC`
	rows, err := r.conn.Query(ctx, q, t.UserID, t.ProfileID)
	if err != nil {
		return nil, fmt.Errorf("analytics: study days: %w", err)
	}
	defer func() { _ = rows.Close() }()
	var days []time.Time
	for rows.Next() {
		var d time.Time
		if err := rows.Scan(&d); err != nil {
			return nil, fmt.Errorf("analytics: scan study day: %w", err)
		}
		days = append(days, d)
	}
	return days, rows.Err()
}

// HeatmapDay is one calendar day's review count.
type HeatmapDay struct {
	Date    string // YYYY-MM-DD
	Reviews int64
}

// Heatmap returns per-day review counts within [start, end).
func (r *Repo) Heatmap(ctx context.Context, t Tenant, start, end time.Time) ([]HeatmapDay, error) {
	const q = `SELECT toString(toDate(ts)) AS day, toInt64(count())
		FROM review_events FINAL
		WHERE user_id = ? AND profile_id = ? AND ts >= ? AND ts < ?
		GROUP BY day ORDER BY day`
	rows, err := r.conn.Query(ctx, q, t.UserID, t.ProfileID, start, end)
	if err != nil {
		return nil, fmt.Errorf("analytics: heatmap: %w", err)
	}
	defer func() { _ = rows.Close() }()
	var out []HeatmapDay
	for rows.Next() {
		var d HeatmapDay
		if err := rows.Scan(&d.Date, &d.Reviews); err != nil {
			return nil, fmt.Errorf("analytics: scan heatmap day: %w", err)
		}
		out = append(out, d)
	}
	return out, rows.Err()
}

// Retention is the measured-recall aggregate among due (review-state) cards.
type Retention struct {
	DueReviews int64
	DueCorrect int64
}

// Retention counts reviews of cards that were in the review state (prev_state =
// 2) and how many were recalled (grade > Again), since `since`, optionally
// scoped to one deck.
func (r *Repo) Retention(ctx context.Context, t Tenant, deckID string, since time.Time) (Retention, error) {
	q := `SELECT toInt64(countIf(prev_state = 2)), toInt64(countIf(prev_state = 2 AND grade > 1))
		FROM review_events FINAL
		WHERE user_id = ? AND profile_id = ? AND ts >= ?`
	args := []any{t.UserID, t.ProfileID, since}
	if deckID != "" {
		q += ` AND deck_id = ?`
		args = append(args, deckID)
	}
	var ret Retention
	row := r.conn.QueryRow(ctx, q, args...)
	if err := row.Scan(&ret.DueReviews, &ret.DueCorrect); err != nil {
		return Retention{}, fmt.Errorf("analytics: retention: %w", err)
	}
	return ret, nil
}

// Bucket is one distribution row for a breakdown query.
type Bucket struct {
	Key     string
	Reviews int64
	Correct int64
	TimeMs  int64
}

// Breakdown groups reviews since `since` by deck_id, the card state at review
// time (prev_state), or hour-of-day. `by` must be one of "deck", "state",
// "hour"; an unknown value returns an error.
func (r *Repo) Breakdown(ctx context.Context, t Tenant, by string, since time.Time) ([]Bucket, error) {
	var keyExpr string
	switch by {
	case "deck":
		keyExpr = "deck_id"
	case "state":
		keyExpr = "toString(prev_state)"
	case "hour":
		keyExpr = "toString(toHour(ts))"
	default:
		return nil, fmt.Errorf("analytics: invalid breakdown dimension %q", by)
	}

	// keyExpr is a fixed, validated expression (never user input), so it is safe
	// to interpolate into the GROUP BY.
	q := fmt.Sprintf(`SELECT %s AS k, toInt64(count()), toInt64(countIf(grade > 1)), toInt64(sum(time_ms))
		FROM review_events FINAL
		WHERE user_id = ? AND profile_id = ? AND ts >= ?
		GROUP BY k ORDER BY k`, keyExpr)
	rows, err := r.conn.Query(ctx, q, t.UserID, t.ProfileID, since)
	if err != nil {
		return nil, fmt.Errorf("analytics: breakdown: %w", err)
	}
	defer func() { _ = rows.Close() }()
	var out []Bucket
	for rows.Next() {
		var b Bucket
		if err := rows.Scan(&b.Key, &b.Reviews, &b.Correct, &b.TimeMs); err != nil {
			return nil, fmt.Errorf("analytics: scan bucket: %w", err)
		}
		out = append(out, b)
	}
	return out, rows.Err()
}
