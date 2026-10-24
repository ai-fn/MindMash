//go:build integration

package usn

import (
	"context"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/stretchr/testify/require"
	"github.com/testcontainers/testcontainers-go/modules/postgres"

	platformdb "github.com/mindmesh/mindmesh/internal/platform/db"
)

func TestNextAndCurrent(t *testing.T) {
	ctx := context.Background()
	pgc, err := postgres.Run(ctx, "postgres:18-alpine", postgres.BasicWaitStrategies())
	require.NoError(t, err)
	t.Cleanup(func() { _ = pgc.Terminate(ctx) })
	dsn, err := pgc.ConnectionString(ctx, "sslmode=disable")
	require.NoError(t, err)
	require.NoError(t, platformdb.Migrate(ctx, dsn, "../../../migrations", "platform"))
	pool, err := pgxpool.New(ctx, dsn)
	require.NoError(t, err)
	t.Cleanup(pool.Close)

	user, other := uuid.NewString(), uuid.NewString()
	cur, err := Current(ctx, pool, user)
	require.NoError(t, err)
	require.Zero(t, cur, "a user who never wrote is at 0")

	for want := int64(1); want <= 3; want++ {
		got, err := Next(ctx, pool, user)
		require.NoError(t, err)
		require.Equal(t, want, got)
	}
	cur, err = Current(ctx, pool, user)
	require.NoError(t, err)
	require.Equal(t, int64(3), cur)

	got, err := Next(ctx, pool, other)
	require.NoError(t, err)
	require.Equal(t, int64(1), got, "counters are per user")
}
