//go:build integration

package sync

import (
	"context"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/redis/go-redis/v9"
	"github.com/testcontainers/testcontainers-go"
	"github.com/testcontainers/testcontainers-go/modules/postgres"
	tcredis "github.com/testcontainers/testcontainers-go/modules/redis"
	"github.com/testcontainers/testcontainers-go/wait"
	"go.uber.org/zap"

	platformdb "github.com/mindmesh/mindmesh/internal/platform/db"
	"github.com/mindmesh/mindmesh/internal/platform/ids"
)

const (
	testUserID    = "00000000-0000-0000-0000-000000000001"
	testProfileID = "00000000-0000-0000-0000-000000000002"
)

type integrationComponents struct {
	pool *pgxpool.Pool
	rdb  *redis.Client
	repo *PostgresRepository
	svc  *Service
}

func setupIntegration(t *testing.T) integrationComponents {
	t.Helper()
	ctx := context.Background()

	// The postgres image restarts its server process after initdb; without an
	// explicit occurrence count the default wait strategy can report ready
	// after the first (pre-restart) boot, so the first connection attempt
	// hits a server that's about to restart ("connection reset by peer").
	pgCtr, err := postgres.Run(ctx, "postgres:18-alpine",
		postgres.WithDatabase("sync_test"),
		postgres.WithUsername("test"),
		postgres.WithPassword("test"),
		testcontainers.WithWaitStrategy(
			wait.ForLog("database system is ready to accept connections").
				WithOccurrence(2).
				WithStartupTimeout(2*time.Minute),
		),
	)
	if err != nil {
		t.Fatalf("start postgres: %v", err)
	}
	t.Cleanup(func() { _ = pgCtr.Terminate(ctx) })

	redisCtr, err := tcredis.Run(ctx, "redis:8-alpine")
	if err != nil {
		t.Fatalf("start redis: %v", err)
	}
	t.Cleanup(func() { _ = redisCtr.Terminate(ctx) })

	dsn, err := pgCtr.ConnectionString(ctx, "sslmode=disable")
	if err != nil {
		t.Fatalf("pg connection string: %v", err)
	}
	// Sync owns no tables of its own; it reads Collection's schema directly.
	if err := platformdb.RunMigrations(ctx, dsn, "../../migrations/collection"); err != nil {
		t.Fatalf("run collection migrations: %v", err)
	}

	pool, err := pgxpool.New(ctx, dsn)
	if err != nil {
		t.Fatalf("pgxpool: %v", err)
	}
	t.Cleanup(pool.Close)

	redisAddr, err := redisCtr.Endpoint(ctx, "")
	if err != nil {
		t.Fatalf("redis endpoint: %v", err)
	}
	rdb := redis.NewClient(&redis.Options{Addr: redisAddr})
	t.Cleanup(func() { _ = rdb.Close() })

	repo := NewPostgresRepository(pool)
	svc := NewService(repo, rdb, zap.NewNop())
	return integrationComponents{pool: pool, rdb: rdb, repo: repo, svc: svc}
}

// ─── Fixtures ────────────────────────────────────────────────────────────────
// Sync shares Collection's tables and writes none of them in production, so
// fixtures are inserted with raw SQL mirroring the migrations/collection
// schema, exactly as internal/scheduler's integration tests do.

func bumpUSN(t *testing.T, c integrationComponents) int64 {
	t.Helper()
	var usn int64
	err := c.pool.QueryRow(context.Background(), `
		INSERT INTO user_usn (user_id, usn) VALUES ($1, 1)
		ON CONFLICT (user_id) DO UPDATE SET usn = user_usn.usn + 1
		RETURNING usn`, testUserID).Scan(&usn)
	if err != nil {
		t.Fatalf("bump usn: %v", err)
	}
	return usn
}

func insertNoteType(t *testing.T, c integrationComponents, usn int64) string {
	t.Helper()
	id := ids.NewUUID()
	_, err := c.pool.Exec(context.Background(), `
		INSERT INTO note_types (id, user_id, profile_id, name, fields, card_templates, css, usn)
		VALUES ($1, $2, $3, 'Basic', '[{"name":"Front"},{"name":"Back"}]', '[{"name":"Card 1","front":"{{Front}}","back":"{{Back}}"}]', '', $4)`,
		id, testUserID, testProfileID, usn)
	if err != nil {
		t.Fatalf("insert note_type: %v", err)
	}
	return id
}

func insertNote(t *testing.T, c integrationComponents, noteTypeID string, usn int64) string {
	t.Helper()
	id := ids.NewUUID()
	_, err := c.pool.Exec(context.Background(), `
		INSERT INTO notes (id, user_id, profile_id, note_type_id, fields, usn)
		VALUES ($1, $2, $3, $4, '{"Front":"hi","Back":"bye"}', $5)`,
		id, testUserID, testProfileID, noteTypeID, usn)
	if err != nil {
		t.Fatalf("insert note: %v", err)
	}
	return id
}

func insertTombstone(t *testing.T, c integrationComponents, typ, entityID string, usn int64) {
	t.Helper()
	_, err := c.pool.Exec(context.Background(), `
		INSERT INTO tombstones (user_id, type, id, usn) VALUES ($1, $2, $3, $4)`,
		testUserID, typ, entityID, usn)
	if err != nil {
		t.Fatalf("insert tombstone: %v", err)
	}
}

// ─── GetChanges returns exactly the expected delta ───────────────────────────

func TestGetChanges_ReturnsExpectedDeltaAndTombstones(t *testing.T) {
	c := setupIntegration(t)
	ctx := context.Background()

	usn1 := bumpUSN(t, c)
	nt := insertNoteType(t, c, usn1)

	usn2 := bumpUSN(t, c)
	insertNote(t, c, nt, usn2)

	usn3 := bumpUSN(t, c)
	insertTombstone(t, c, EntityNote, ids.NewUUID(), usn3)

	cs, latest, next, err := c.svc.GetChanges(ctx, testUserID, 0, "")
	if err != nil {
		t.Fatalf("GetChanges: %v", err)
	}
	if latest != usn3 {
		t.Fatalf("want latest_usn=%d, got %d", usn3, latest)
	}
	if next != "" {
		t.Fatalf("want no pagination for 3 rows, got %q", next)
	}
	if len(cs.NoteTypes) != 1 || cs.NoteTypes[0].ID != nt {
		t.Fatalf("want note_type %s in delta, got %+v", nt, cs.NoteTypes)
	}
	if len(cs.NoteTypes[0].Fields) != 2 || cs.NoteTypes[0].Fields[0].Name != "Front" || cs.NoteTypes[0].Fields[1].Name != "Back" {
		t.Fatalf("want note_type Fields=[Front,Back], got %+v", cs.NoteTypes[0].Fields)
	}
	if len(cs.NoteTypes[0].CardTemplates) != 1 || cs.NoteTypes[0].CardTemplates[0].Front != "{{Front}}" {
		t.Fatalf("want note_type CardTemplates[0].Front=\"{{Front}}\", got %+v", cs.NoteTypes[0].CardTemplates)
	}
	if len(cs.Notes) != 1 {
		t.Fatalf("want 1 note in delta, got %+v", cs.Notes)
	}
	if len(cs.Deleted) != 1 {
		t.Fatalf("want 1 tombstone in delta, got %+v", cs.Deleted)
	}

	// Pulling again from the new latest_usn converges to no further changes.
	cs2, latest2, _, err := c.svc.GetChanges(ctx, testUserID, latest, "")
	if err != nil {
		t.Fatalf("GetChanges (converged): %v", err)
	}
	if latest2 != latest {
		t.Fatalf("want latest_usn unchanged on a no-op pull, got %d vs %d", latest2, latest)
	}
	if len(cs2.NoteTypes)+len(cs2.Notes)+len(cs2.Deleted) != 0 {
		t.Fatalf("want empty delta once caught up, got %+v", cs2)
	}
}

// ─── Two clients diverge then converge ───────────────────────────────────────

func TestGetChanges_TwoClientsConvergeToSameLatestUSN(t *testing.T) {
	c := setupIntegration(t)
	ctx := context.Background()

	usn1 := bumpUSN(t, c)
	nt := insertNoteType(t, c, usn1)
	usn2 := bumpUSN(t, c)
	insertNote(t, c, nt, usn2)

	// Client A starts from scratch; client B already saw usn1.
	csA, latestA, _, err := c.svc.GetChanges(ctx, testUserID, 0, "")
	if err != nil {
		t.Fatalf("client A GetChanges: %v", err)
	}
	csB, latestB, _, err := c.svc.GetChanges(ctx, testUserID, usn1, "")
	if err != nil {
		t.Fatalf("client B GetChanges: %v", err)
	}

	if latestA != latestB {
		t.Fatalf("want both clients to converge on the same latest_usn, got %d vs %d", latestA, latestB)
	}
	if len(csA.NoteTypes) != 1 || len(csA.Notes) != 1 {
		t.Fatalf("client A should see both entities, got %+v", csA)
	}
	if len(csB.NoteTypes) != 0 || len(csB.Notes) != 1 {
		t.Fatalf("client B (already at usn1) should see only the note, got %+v", csB)
	}
}

// ─── Realtime notifier pub/sub (Redis-backed) ────────────────────────────────

func TestSubscribeNotify_DeliversToMatchingUserOnly(t *testing.T) {
	c := setupIntegration(t)
	ctx := context.Background()

	chA, unsubA, err := c.svc.Subscribe(ctx, "userA")
	if err != nil {
		t.Fatalf("Subscribe userA: %v", err)
	}
	defer unsubA()
	chB, unsubB, err := c.svc.Subscribe(ctx, "userB")
	if err != nil {
		t.Fatalf("Subscribe userB: %v", err)
	}
	defer unsubB()

	if err := c.svc.Notify(ctx, "userA"); err != nil {
		t.Fatalf("Notify userA: %v", err)
	}

	select {
	case <-chA:
	case <-time.After(5 * time.Second):
		t.Fatal("want userA's subscriber to be notified")
	}

	select {
	case <-chB:
		t.Fatal("userB's subscriber must not be notified by userA's event")
	case <-time.After(100 * time.Millisecond):
	}
}

// TestSubscribeNotify_WorksAcrossReplicas is the regression test for the bug
// where Subscribe/Notify was an in-process map: it didn't work once Sync ran
// as more than one replica, because a Notify on replica B's Service never
// reached an SSE client subscribed on replica A's Service. Two independent
// Service instances sharing one Redis (as every replica does in production)
// stand in for "replica A" and "replica B" here.
func TestSubscribeNotify_WorksAcrossReplicas(t *testing.T) {
	c := setupIntegration(t)
	ctx := context.Background()

	replicaA := NewService(c.repo, c.rdb, zap.NewNop())
	replicaB := NewService(c.repo, c.rdb, zap.NewNop())

	ch, unsub, err := replicaA.Subscribe(ctx, "userA")
	if err != nil {
		t.Fatalf("Subscribe on replica A: %v", err)
	}
	defer unsub()

	if err := replicaB.Notify(ctx, "userA"); err != nil {
		t.Fatalf("Notify on replica B: %v", err)
	}

	select {
	case <-ch:
	case <-time.After(5 * time.Second):
		t.Fatal("want a Notify on replica B to reach a subscriber on replica A")
	}
}

func TestUnsubscribe_ClosesChannel(t *testing.T) {
	c := setupIntegration(t)
	ctx := context.Background()

	ch, unsub, err := c.svc.Subscribe(ctx, "userA")
	if err != nil {
		t.Fatalf("Subscribe: %v", err)
	}
	unsub()

	select {
	case _, open := <-ch:
		if open {
			t.Fatal("want channel closed after unsubscribe")
		}
	case <-time.After(5 * time.Second):
		t.Fatal("want channel closed shortly after unsubscribe")
	}
}

func TestUnsubscribe_IsIdempotent(t *testing.T) {
	c := setupIntegration(t)
	ctx := context.Background()

	_, unsub, err := c.svc.Subscribe(ctx, "userA")
	if err != nil {
		t.Fatalf("Subscribe: %v", err)
	}
	unsub()
	unsub() // must not panic
}
