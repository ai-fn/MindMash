// Package usn owns the per-user update sequence number (user_usn) that orders every
// service's writes in the sync change feed.
package usn

import (
	"context"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"
)

// Querier is satisfied by *pgxpool.Pool, *pgx.Conn and pgx.Tx.
type Querier interface {
	QueryRow(ctx context.Context, sql string, args ...any) pgx.Row
}

// Next bumps and returns userID's USN. Call it in the write's transaction: the row lock
// serialises a user's writers until commit.
func Next(ctx context.Context, q Querier, userID string) (int64, error) {
	var n int64
	err := q.QueryRow(ctx, `
		INSERT INTO user_usn (user_id, usn) VALUES ($1, 1)
		ON CONFLICT (user_id) DO UPDATE SET usn = user_usn.usn + 1
		RETURNING usn`, userID).Scan(&n)
	if err != nil {
		return 0, fmt.Errorf("usn: next: %w", err)
	}
	return n, nil
}

// Current returns userID's latest USN, 0 if the user has never written.
func Current(ctx context.Context, q Querier, userID string) (int64, error) {
	var n int64
	err := q.QueryRow(ctx, `SELECT usn FROM user_usn WHERE user_id = $1`, userID).Scan(&n)
	if errors.Is(err, pgx.ErrNoRows) {
		return 0, nil
	}
	if err != nil {
		return 0, fmt.Errorf("usn: current: %w", err)
	}
	return n, nil
}
