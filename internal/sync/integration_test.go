//go:build integration

package sync

import (
	"context"
	"net"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/redis/go-redis/v9"
	"github.com/testcontainers/testcontainers-go"
	"github.com/testcontainers/testcontainers-go/modules/postgres"
	tcredis "github.com/testcontainers/testcontainers-go/modules/redis"
	"github.com/testcontainers/testcontainers-go/wait"
	"go.uber.org/zap"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/test/bufconn"

	collectionv1 "github.com/mindmesh/mindmesh/api/collection/v1"
	"github.com/mindmesh/mindmesh/internal/collection"
	"github.com/mindmesh/mindmesh/internal/platform/auth"
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

// withTestIdentity is a server-side unary interceptor standing in for
// Collection's production auth interceptor: in production, a JWT is
// validated and auth.WithIdentity is populated by the gateway/middleware
// before the handler runs (see internal/platform/auth/middleware.go). For
// this in-process bufconn harness there is no real JWT — the simplest
// correct equivalent is to inject a fixed IdentityContext directly into the
// context, exactly as the interceptor would have, so Collection's handlers
// see the same id.UserID/id.ProfileID a real authenticated call would carry.
func withTestIdentity(userID, profileID string) grpc.UnaryServerInterceptor {
	return func(ctx context.Context, req any, info *grpc.UnaryServerInfo, handler grpc.UnaryHandler) (any, error) {
		ctx = auth.WithIdentity(ctx, auth.IdentityContext{UserID: userID, ProfileID: profileID})
		return handler(ctx, req)
	}
}

// setupCollectionBufconn stands up a real internal/collection.Handler (the
// same gRPC server Collection runs in production) over an in-process
// bufconn listener, wired to the same Postgres pool the test already uses
// for Collection's tables, and returns a CollectionClient dialed against it
// via the real sync.NewGRPCCollectionClient — so Apply exercises Collection's
// actual optimistic-concurrency/outbox/USN code, not a fake.
func setupCollectionBufconn(t *testing.T, pool *pgxpool.Pool, rdb *redis.Client) CollectionClient {
	t.Helper()

	collRepo := collection.NewPostgresRepository(pool)
	collSvc := collection.NewService(collRepo, rdb, zap.NewNop())
	collHandler := collection.NewHandler(collSvc)

	lis := bufconn.Listen(1024 * 1024)
	t.Cleanup(func() { _ = lis.Close() })

	srv := grpc.NewServer(grpc.ChainUnaryInterceptor(withTestIdentity(testUserID, testProfileID)))
	collectionv1.RegisterNoteTypeServiceServer(srv, collHandler)
	collectionv1.RegisterNoteServiceServer(srv, collHandler)
	collectionv1.RegisterDeckServiceServer(srv, collHandler)
	collectionv1.RegisterFilteredDeckServiceServer(srv, collHandler)

	go func() { _ = srv.Serve(lis) }()
	t.Cleanup(srv.Stop)

	conn, err := grpc.NewClient("passthrough:///bufconn",
		grpc.WithContextDialer(func(ctx context.Context, _ string) (net.Conn, error) { return lis.DialContext(ctx) }),
		grpc.WithTransportCredentials(insecure.NewCredentials()),
	)
	if err != nil {
		t.Fatalf("dial bufconn collection server: %v", err)
	}
	t.Cleanup(func() { _ = conn.Close() })

	return NewGRPCCollectionClient(conn)
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
	// Sync reads Collection's schema directly for the pull path, and owns one
	// table of its own (sync_applied_ops, migrations/sync) for push-path
	// op_id idempotency — both sets of migrations must be applied here. Each
	// uses its own x-migrations-table so the two migration histories don't
	// collide on the shared DB, mirroring cmd/sync/main.go's migrations block.
	if err := platformdb.RunMigrations(ctx, dsn, "../../migrations/collection"); err != nil {
		t.Fatalf("run collection migrations: %v", err)
	}
	syncDSN := dsn + "&x-migrations-table=schema_migrations_sync"
	if err := platformdb.RunMigrations(ctx, syncDSN, "../../migrations/sync"); err != nil {
		t.Fatalf("run sync migrations: %v", err)
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

	coll := setupCollectionBufconn(t, pool, rdb)

	repo := NewPostgresRepository(pool)
	svc := NewService(repo, coll, rdb, zap.NewNop())
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

// insertDeck inserts a minimal root deck fixture. Apply-path tests need a
// real deck because Collection's UpdateNote regenerates the note's cards on
// every update (see internal/collection/service.go's GenerateCardDiff call),
// and cards.deck_id is NOT NULL — so any note fixture exercised through
// Apply must already have at least one card pointing at a real deck.
func insertDeck(t *testing.T, c integrationComponents, usn int64) string {
	t.Helper()
	id := ids.NewUUID()
	_, err := c.pool.Exec(context.Background(), `
		INSERT INTO decks (id, user_id, profile_id, name, path, usn)
		VALUES ($1, $2, $3, 'Default', 'default', $4)`,
		id, testUserID, testProfileID, usn)
	if err != nil {
		t.Fatalf("insert deck: %v", err)
	}
	return id
}

// insertCard inserts a single card for noteID/deckID at template ordinal 0,
// matching the one card Collection's own cardgen produces for the "Basic"
// note type fixture insertNoteType creates (one card template).
func insertCard(t *testing.T, c integrationComponents, noteID, deckID string, usn int64) string {
	t.Helper()
	id := ids.NewUUID()
	_, err := c.pool.Exec(context.Background(), `
		INSERT INTO cards (id, user_id, profile_id, note_id, deck_id, template_ord, usn)
		VALUES ($1, $2, $3, $4, $5, 0, $6)`,
		id, testUserID, testProfileID, noteID, deckID, usn)
	if err != nil {
		t.Fatalf("insert card: %v", err)
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

	replicaA := NewService(c.repo, nil, c.rdb, zap.NewNop())
	replicaB := NewService(c.repo, nil, c.rdb, zap.NewNop())

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

// ─── Apply (push path) through a real Collection ────────────────────────────
// These tests dial a real internal/collection.Handler over bufconn (see
// setupCollectionBufconn) instead of faking CollectionClient, so they
// exercise Collection's actual optimistic-concurrency/outbox/USN code the
// way service_test.go's fakes structurally cannot.

func TestApply_Integration_FastForward(t *testing.T) {
	c := setupIntegration(t)
	ctx := context.Background()

	usn1 := bumpUSN(t, c)
	ntID := insertNoteType(t, c, usn1)
	usnDeck := bumpUSN(t, c)
	deckID := insertDeck(t, c, usnDeck)
	usn2 := bumpUSN(t, c)
	noteID := insertNote(t, c, ntID, usn2)
	usnCard := bumpUSN(t, c)
	insertCard(t, c, noteID, deckID, usnCard)

	mutation := Mutation{
		OpID:     "op-fast-forward-1",
		Type:     MutationNoteUpdate,
		EntityID: noteID,
		BaseUSN:  usn2, // matches the note's current server usn: fast-forward.
		Note: &NotePayload{
			Fields: map[string]string{"Front": "updated front", "Back": "bye"},
			Tags:   []string{"tag1"},
		},
	}

	results, latestUSN, err := c.svc.Apply(ctx, testUserID, []Mutation{mutation})
	if err != nil {
		t.Fatalf("Apply: %v", err)
	}
	if len(results) != 1 {
		t.Fatalf("want 1 result, got %d", len(results))
	}
	res := results[0]
	if res.Outcome != OutcomeApplied {
		t.Fatalf("want OutcomeApplied, got %v (reason=%q)", res.Outcome, res.Reason)
	}
	if res.NewUSN != latestUSN {
		t.Fatalf("want result.NewUSN=%d to match response latest_usn=%d", res.NewUSN, latestUSN)
	}
	if res.Note == nil || res.Note.Fields["Front"] != "updated front" {
		t.Fatalf("want returned note to reflect the update, got %+v", res.Note)
	}

	// Assert the note actually changed in Postgres, not just in the response.
	var front string
	var dbUSN int64
	err = c.pool.QueryRow(ctx, `SELECT fields->>'Front', usn FROM notes WHERE id = $1`, noteID).Scan(&front, &dbUSN)
	if err != nil {
		t.Fatalf("query note: %v", err)
	}
	if front != "updated front" {
		t.Fatalf("want note.fields.Front=%q in Postgres, got %q", "updated front", front)
	}
	if dbUSN != res.NewUSN {
		t.Fatalf("want note.usn=%d in Postgres to match result.NewUSN=%d", dbUSN, res.NewUSN)
	}
}

// TestApply_Integration_TwoClientsConvergeViaLWW: client A and B both read the
// note at usn2/base_usn=usn2. A applies first (fast-forward, bumping the
// note's usn past base_usn). B's apply now races against a higher server usn,
// so it resolves via last-write-wins on mtime — and afterward a GetChanges
// pull converges both clients to whichever side's mtime won.
func TestApply_Integration_TwoClientsConvergeViaLWW(t *testing.T) {
	c := setupIntegration(t)
	ctx := context.Background()

	usn1 := bumpUSN(t, c)
	ntID := insertNoteType(t, c, usn1)
	usnDeck := bumpUSN(t, c)
	deckID := insertDeck(t, c, usnDeck)
	usn2 := bumpUSN(t, c)
	noteID := insertNote(t, c, ntID, usn2)
	usnCard := bumpUSN(t, c)
	insertCard(t, c, noteID, deckID, usnCard)

	baseUSN := usn2
	earlier := time.Now().UTC().Add(-1 * time.Hour)
	later := time.Now().UTC().Add(1 * time.Hour)

	// Client A applies first: fast-forward (base_usn matches server usn).
	resA, _, err := c.svc.Apply(ctx, testUserID, []Mutation{{
		OpID:        "op-clientA",
		Type:        MutationNoteUpdate,
		EntityID:    noteID,
		BaseUSN:     baseUSN,
		ClientMtime: earlier,
		Note:        &NotePayload{Fields: map[string]string{"Front": "client A wins later check", "Back": "bye"}},
	}})
	if err != nil {
		t.Fatalf("client A Apply: %v", err)
	}
	if resA[0].Outcome != OutcomeApplied {
		t.Fatalf("want client A OutcomeApplied, got %v (reason=%q)", resA[0].Outcome, resA[0].Reason)
	}

	// Client B started from the same base_usn but its client_mtime is later
	// than the server's current mtime (set by A's write, which just happened),
	// so LWW resolves client-wins: B's mutation is merged in.
	resB, _, err := c.svc.Apply(ctx, testUserID, []Mutation{{
		OpID:        "op-clientB",
		Type:        MutationNoteUpdate,
		EntityID:    noteID,
		BaseUSN:     baseUSN, // stale: A already bumped the server's usn past this.
		ClientMtime: later,
		Note:        &NotePayload{Fields: map[string]string{"Front": "client B newer wins", "Back": "bye"}},
	}})
	if err != nil {
		t.Fatalf("client B Apply: %v", err)
	}
	if resB[0].Outcome != OutcomeMerged {
		t.Fatalf("want client B OutcomeMerged (LWW client-wins), got %v (reason=%q)", resB[0].Outcome, resB[0].Reason)
	}
	if resB[0].Note == nil || resB[0].Note.Fields["Front"] != "client B newer wins" {
		t.Fatalf("want B's payload to have won, got %+v", resB[0].Note)
	}

	// Postgres state matches the mtime winner: client B.
	var front string
	if err := c.pool.QueryRow(ctx, `SELECT fields->>'Front' FROM notes WHERE id = $1`, noteID).Scan(&front); err != nil {
		t.Fatalf("query note: %v", err)
	}
	if front != "client B newer wins" {
		t.Fatalf("want Postgres note.fields.Front=%q, got %q", "client B newer wins", front)
	}

	// A subsequent GetChanges pull (by either client, from before either
	// write) converges to this same final state.
	cs, _, _, err := c.svc.GetChanges(ctx, testUserID, usn1, "")
	if err != nil {
		t.Fatalf("GetChanges: %v", err)
	}
	var found bool
	for _, n := range cs.Notes {
		if n.ID == noteID {
			found = true
			if n.Fields["Front"] != "client B newer wins" {
				t.Fatalf("want GetChanges to converge on %q, got %q", "client B newer wins", n.Fields["Front"])
			}
		}
	}
	if !found {
		t.Fatalf("want note %s in GetChanges delta, got %+v", noteID, cs.Notes)
	}
}

// TestApply_Integration_OpIDReplayIsIdempotent: calling Apply twice with the
// identical Mutation (same op_id) must not double-bump usn on the second
// call, and must report OutcomeReplayed.
func TestApply_Integration_OpIDReplayIsIdempotent(t *testing.T) {
	c := setupIntegration(t)
	ctx := context.Background()

	usn1 := bumpUSN(t, c)
	ntID := insertNoteType(t, c, usn1)
	usnDeck := bumpUSN(t, c)
	deckID := insertDeck(t, c, usnDeck)
	usn2 := bumpUSN(t, c)
	noteID := insertNote(t, c, ntID, usn2)
	usnCard := bumpUSN(t, c)
	insertCard(t, c, noteID, deckID, usnCard)

	mutation := Mutation{
		OpID:     "op-replay-1",
		Type:     MutationNoteUpdate,
		EntityID: noteID,
		BaseUSN:  usn2,
		Note:     &NotePayload{Fields: map[string]string{"Front": "replay me", "Back": "bye"}},
	}

	res1, _, err := c.svc.Apply(ctx, testUserID, []Mutation{mutation})
	if err != nil {
		t.Fatalf("first Apply: %v", err)
	}
	if res1[0].Outcome != OutcomeApplied {
		t.Fatalf("want first call OutcomeApplied, got %v (reason=%q)", res1[0].Outcome, res1[0].Reason)
	}
	firstUSN := res1[0].NewUSN

	usnAfterFirst, err := c.repo.LatestUSN(ctx, testUserID)
	if err != nil {
		t.Fatalf("LatestUSN after first Apply: %v", err)
	}

	// Replay the identical mutation (same op_id).
	res2, _, err := c.svc.Apply(ctx, testUserID, []Mutation{mutation})
	if err != nil {
		t.Fatalf("second (replay) Apply: %v", err)
	}
	if res2[0].Outcome != OutcomeReplayed {
		t.Fatalf("want second call OutcomeReplayed, got %v (reason=%q)", res2[0].Outcome, res2[0].Reason)
	}
	if res2[0].NewUSN != firstUSN {
		t.Fatalf("want replayed NewUSN=%d to match first call's %d", res2[0].NewUSN, firstUSN)
	}

	usnAfterReplay, err := c.repo.LatestUSN(ctx, testUserID)
	if err != nil {
		t.Fatalf("LatestUSN after replay: %v", err)
	}
	if usnAfterReplay != usnAfterFirst {
		t.Fatalf("want replay not to bump usn: before=%d after=%d", usnAfterFirst, usnAfterReplay)
	}
}

// TestApply_Integration_DeleteProducesTombstone: deleting a note through
// Apply must write a tombstone Collection's own delete path produces, and a
// GetChanges pull from before that usn must surface it — tying the push and
// pull paths together end to end.
func TestApply_Integration_DeleteProducesTombstone(t *testing.T) {
	c := setupIntegration(t)
	ctx := context.Background()

	usn1 := bumpUSN(t, c)
	ntID := insertNoteType(t, c, usn1)
	usnDeck := bumpUSN(t, c)
	deckID := insertDeck(t, c, usnDeck)
	usn2 := bumpUSN(t, c)
	noteID := insertNote(t, c, ntID, usn2)
	usnCard := bumpUSN(t, c)
	insertCard(t, c, noteID, deckID, usnCard)

	beforeDeleteUSN := usn2

	res, _, err := c.svc.Apply(ctx, testUserID, []Mutation{{
		OpID:     "op-delete-1",
		Type:     MutationNoteDelete,
		EntityID: noteID,
		BaseUSN:  usn2,
	}})
	if err != nil {
		t.Fatalf("Apply (delete): %v", err)
	}
	if res[0].Outcome != OutcomeApplied {
		t.Fatalf("want OutcomeApplied for delete, got %v (reason=%q)", res[0].Outcome, res[0].Reason)
	}

	// The note row itself is gone.
	var count int
	if err := c.pool.QueryRow(ctx, `SELECT count(*) FROM notes WHERE id = $1`, noteID).Scan(&count); err != nil {
		t.Fatalf("query notes: %v", err)
	}
	if count != 0 {
		t.Fatalf("want note %s gone from notes table, found %d rows", noteID, count)
	}

	// GetChanges from before the delete's usn surfaces a tombstone for it.
	cs, _, _, err := c.svc.GetChanges(ctx, testUserID, beforeDeleteUSN, "")
	if err != nil {
		t.Fatalf("GetChanges: %v", err)
	}
	var found bool
	for _, ts := range cs.Deleted {
		if ts.ID == noteID && ts.Type == EntityNote {
			found = true
		}
	}
	if !found {
		t.Fatalf("want a tombstone for deleted note %s in GetChanges delta, got %+v", noteID, cs.Deleted)
	}
}

// ─── Pagination inside one usn ───────────────────────────────────────────────

func TestGetChanges_PaginatesRowsSharingOneUSN_Postgres(t *testing.T) {
	c := setupIntegration(t)
	ctx := context.Background()
	c.svc.pageSize = 2

	nt := insertNoteType(t, c, bumpUSN(t, c))
	usn := bumpUSN(t, c) // one bulk write: every row below shares it
	want := map[string]bool{}
	for range 5 {
		want[insertNote(t, c, nt, usn)] = true
	}
	wantDeleted := map[string]bool{}
	for range 3 {
		id := ids.NewUUID()
		insertTombstone(t, c, EntityCard, id, usn)
		wantDeleted[id] = true
	}

	got, gotDeleted := map[string]bool{}, map[string]bool{}
	token := ""
	for page := 0; ; page++ {
		if page >= 10 {
			t.Fatal("pagination did not terminate")
		}
		cs, _, next, err := c.svc.GetChanges(ctx, testUserID, 0, token)
		if err != nil {
			t.Fatalf("GetChanges page %d: %v", page, err)
		}
		for _, n := range cs.Notes {
			if got[n.ID] {
				t.Fatalf("note %s returned twice", n.ID)
			}
			got[n.ID] = true
		}
		for _, d := range cs.Deleted {
			if gotDeleted[d.ID] {
				t.Fatalf("tombstone %s returned twice", d.ID)
			}
			gotDeleted[d.ID] = true
		}
		if next == "" {
			break
		}
		token = next
	}
	if len(got) != len(want) {
		t.Fatalf("want all %d notes sharing usn %d, got %d", len(want), usn, len(got))
	}
	if len(gotDeleted) != len(wantDeleted) {
		t.Fatalf("want all %d tombstones sharing usn %d, got %d", len(wantDeleted), usn, len(gotDeleted))
	}
}
