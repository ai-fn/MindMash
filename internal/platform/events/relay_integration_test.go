//go:build integration

package events

import (
	"context"
	"os"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/testcontainers/testcontainers-go/modules/postgres"
	"go.uber.org/zap"
)

type recordingProducer struct {
	msgs []Message
}

func (p *recordingProducer) Publish(_ context.Context, msgs ...Message) error {
	p.msgs = append(p.msgs, msgs...)
	return nil
}

func (p *recordingProducer) Close() error { return nil }

func startOutboxDB(t *testing.T, ctx context.Context) *pgxpool.Pool {
	t.Helper()
	pgc, err := postgres.Run(ctx,
		"postgres:18-alpine",
		postgres.WithDatabase("testdb"),
		postgres.WithUsername("testuser"),
		postgres.WithPassword("testpass"),
		postgres.BasicWaitStrategies(),
	)
	require.NoError(t, err)
	t.Cleanup(func() {
		if err := pgc.Terminate(ctx); err != nil {
			t.Errorf("cleanup: terminate postgres container: %v", err)
		}
	})
	dsn, err := pgc.ConnectionString(ctx, "sslmode=disable")
	require.NoError(t, err)
	pool, err := pgxpool.New(ctx, dsn)
	require.NoError(t, err)
	t.Cleanup(pool.Close)

	ddl, err := os.ReadFile("../../../migrations/collection/000011_outbox.up.sql")
	require.NoError(t, err)
	_, err = pool.Exec(ctx, string(ddl))
	require.NoError(t, err)
	return pool
}

func TestRelayPollMarksPublished(t *testing.T) {
	ctx := context.Background()
	pool := startOutboxDB(t, ctx)

	userA, userB := uuid.NewString(), uuid.NewString()
	entries := []OutboxEntry{
		{ID: uuid.NewString(), UserID: userA, AggregateType: "card", AggregateID: uuid.NewString(), EventType: "card.created", Payload: []byte(`{}`), CreatedAt: time.Now()},
		{ID: uuid.NewString(), UserID: userB, AggregateType: "deck", AggregateID: uuid.NewString(), EventType: "deck.created", Payload: []byte(`{}`), CreatedAt: time.Now()},
	}
	require.NoError(t, pgx.BeginFunc(ctx, pool, func(tx pgx.Tx) error {
		return WriteOutbox(ctx, tx, entries)
	}))

	prod := &recordingProducer{}
	r := NewRelay(pool, prod, zap.NewNop(), RelayConfig{Store: "test"})

	n, err := r.poll(ctx)
	require.NoError(t, err)
	assert.Equal(t, 2, n)
	require.Len(t, prod.msgs, 2)
	for i, e := range entries {
		m := prod.msgs[i]
		assert.Equal(t, e.AggregateType, m.Topic)
		assert.Equal(t, []byte(e.UserID), m.Key)
		assert.Equal(t, e.ID, m.Headers["event_id"])
		assert.Equal(t, e.EventType, m.Headers["event_type"])
	}

	var unpublished int
	require.NoError(t, pool.QueryRow(ctx, `SELECT count(*) FROM outbox WHERE published_at IS NULL`).Scan(&unpublished))
	assert.Zero(t, unpublished)

	n, err = r.poll(ctx)
	require.NoError(t, err)
	assert.Zero(t, n, "published rows must not be re-sent")
	assert.Len(t, prod.msgs, 2)
}
