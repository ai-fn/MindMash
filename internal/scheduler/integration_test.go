//go:build integration

package scheduler

import (
	"context"
	"sync"
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

	collectionv1 "github.com/mindmesh/mindmesh/api/collection/v1"
	platformdb "github.com/mindmesh/mindmesh/internal/platform/db"
	"github.com/mindmesh/mindmesh/internal/platform/ids"
)

// stubCollectionClient embeds the interface so unused methods need no stubs.
type stubCollectionClient struct {
	collectionv1.CollectionInternalServiceClient
}

func (stubCollectionClient) RenderCard(ctx context.Context, in *collectionv1.RenderCardRequest, opts ...grpc.CallOption) (*collectionv1.RenderCardResponse, error) {
	return &collectionv1.RenderCardResponse{FrontHtml: "<div>front:" + in.GetCardId() + "</div>"}, nil
}

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
		postgres.WithDatabase("scheduler_test"),
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
	if err := platformdb.RunMigrations(ctx, dsn, "../../migrations/collection"); err != nil {
		t.Fatalf("run collection migrations: %v", err)
	}
	if err := platformdb.RunMigrations(ctx, dsn+"&x-migrations-table=schema_migrations_scheduler", "../../migrations/scheduler"); err != nil {
		t.Fatalf("run scheduler migrations: %v", err)
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
	svc := NewService(repo, rdb, stubCollectionClient{}, zap.NewNop())
	return integrationComponents{pool: pool, rdb: rdb, repo: repo, svc: svc}
}

const (
	testUserID    = "00000000-0000-0000-0000-000000000001"
	testProfileID = "00000000-0000-0000-0000-000000000002"
)

// ─── Fixtures ────────────────────────────────────────────────────────────────
// The scheduler shares Collection's tables but owns none of their writers in
// this test binary, so fixtures are inserted with raw SQL mirroring the
// migrations/collection schema (note_types, decks, deck_configs, notes, cards).

func insertNoteType(t *testing.T, c integrationComponents) string {
	t.Helper()
	id := ids.NewUUID()
	_, err := c.pool.Exec(context.Background(), `
		INSERT INTO note_types (id, user_id, profile_id, name, fields, card_templates, css)
		VALUES ($1, $2, $3, 'Basic', '[{"name":"Front"},{"name":"Back"}]', '[{"name":"Card 1","front":"{{Front}}","back":"{{Back}}"}]', '')`,
		id, testUserID, testProfileID)
	if err != nil {
		t.Fatalf("insert note_type: %v", err)
	}
	return id
}

// insertDeckConfig creates a deck_configs row with the given daily limits and
// returns its id.
func insertDeckConfig(t *testing.T, c integrationComponents, newPerDay, revPerDay int32) string {
	t.Helper()
	id := ids.NewUUID()
	_, err := c.pool.Exec(context.Background(), `
		INSERT INTO deck_configs (id, user_id, profile_id, name, new_per_day, rev_per_day)
		VALUES ($1, $2, $3, 'cfg', $4, $5)`,
		id, testUserID, testProfileID, newPerDay, revPerDay)
	if err != nil {
		t.Fatalf("insert deck_config: %v", err)
	}
	return id
}

func insertDeck(t *testing.T, c integrationComponents, name string, configID *string) string {
	t.Helper()
	id := ids.NewUUID()
	_, err := c.pool.Exec(context.Background(), `
		INSERT INTO decks (id, user_id, profile_id, name, path, config_id)
		VALUES ($1, $2, $3, $4, $5::ltree, $6)`,
		id, testUserID, testProfileID, name, name, configID)
	if err != nil {
		t.Fatalf("insert deck: %v", err)
	}
	return id
}

func insertNote(t *testing.T, c integrationComponents, noteTypeID string) string {
	t.Helper()
	id := ids.NewUUID()
	_, err := c.pool.Exec(context.Background(), `
		INSERT INTO notes (id, user_id, profile_id, note_type_id, fields)
		VALUES ($1, $2, $3, $4, '{"Front":"hi","Back":"bye"}')`,
		id, testUserID, testProfileID, noteTypeID)
	if err != nil {
		t.Fatalf("insert note: %v", err)
	}
	return id
}

type cardFixture struct {
	state CardState
	due   *time.Time
}

func insertCard(t *testing.T, c integrationComponents, noteID, deckID string, f cardFixture) string {
	t.Helper()
	id := ids.NewUUID()
	_, err := c.pool.Exec(context.Background(), `
		INSERT INTO cards (id, user_id, profile_id, note_id, deck_id, state, queue, due)
		VALUES ($1, $2, $3, $4, $5, $6, 0, $7)`,
		id, testUserID, testProfileID, noteID, deckID, int16(f.state), f.due)
	if err != nil {
		t.Fatalf("insert card: %v", err)
	}
	return id
}

func reviewLogCount(t *testing.T, c integrationComponents, cardID string) int {
	t.Helper()
	var n int
	if err := c.pool.QueryRow(context.Background(),
		`SELECT COUNT(*) FROM review_logs WHERE user_id = $1 AND card_id = $2`, testUserID, cardID).Scan(&n); err != nil {
		t.Fatalf("count review_logs: %v", err)
	}
	return n
}

// ─── Answer writes a review_log and advances the card ────────────────────────

func TestAnswer_RecordsReviewLogAndAdvancesCard(t *testing.T) {
	c := setupIntegration(t)
	ctx := context.Background()

	nt := insertNoteType(t, c)
	deck := insertDeck(t, c, "deck1", nil)
	note := insertNote(t, c, nt)
	cardID := insertCard(t, c, note, deck, cardFixture{state: StateNew})

	updated, preview, err := c.svc.Answer(ctx, testUserID, testProfileID, cardID, Good, 1500)
	if err != nil {
		t.Fatalf("Answer: %v", err)
	}
	if updated.State == StateNew {
		t.Fatalf("want state advanced past New, got %v", updated.State)
	}
	if updated.Due == nil {
		t.Fatal("want Due to be set after answering")
	}
	if preview.Again == 0 && preview.Hard == 0 && preview.Good == 0 && preview.Easy == 0 {
		t.Fatal("want non-zero interval preview")
	}
	if n := reviewLogCount(t, c, cardID); n != 1 {
		t.Fatalf("want 1 review_logs row, got %d", n)
	}
}

// ─── review_logs count matches the number of answers ─────────────────────────

func TestReviewLogsCountMatchesAnswers(t *testing.T) {
	c := setupIntegration(t)
	ctx := context.Background()

	nt := insertNoteType(t, c)
	deck := insertDeck(t, c, "deck1", nil)
	note := insertNote(t, c, nt)
	cardID := insertCard(t, c, note, deck, cardFixture{state: StateNew})

	const answers = 4
	for i := 0; i < answers; i++ {
		if _, _, err := c.svc.Answer(ctx, testUserID, testProfileID, cardID, Good, 100); err != nil {
			t.Fatalf("Answer #%d: %v", i, err)
		}
	}
	if n := reviewLogCount(t, c, cardID); n != answers {
		t.Fatalf("want %d review_logs rows, got %d", answers, n)
	}
}

// ─── Daily new-card limit holds under concurrent BuildQueue calls ────────────

func TestBuildQueue_DailyNewLimitEnforcedConcurrently(t *testing.T) {
	c := setupIntegration(t)
	ctx := context.Background()

	cfg := insertDeckConfig(t, c, 1, 200) // only 1 new card/day allowed
	deck := insertDeck(t, c, "deck1", &cfg)
	nt := insertNoteType(t, c)

	const totalCards = 5
	for i := 0; i < totalCards; i++ {
		note := insertNote(t, c, nt)
		insertCard(t, c, note, deck, cardFixture{state: StateNew})
	}

	const workers = 5
	var wg sync.WaitGroup
	served := make([]int, workers)
	for i := 0; i < workers; i++ {
		wg.Add(1)
		go func(idx int) {
			defer wg.Done()
			queue, err := c.svc.BuildQueue(ctx, testUserID, testProfileID, deck, 10)
			if err != nil {
				t.Errorf("BuildQueue worker %d: %v", idx, err)
				return
			}
			served[idx] = len(queue)
		}(i)
	}
	wg.Wait()

	total := 0
	for _, n := range served {
		total += n
	}
	if total != 1 {
		t.Fatalf("want exactly 1 new card served across all concurrent BuildQueue calls (new_per_day=1), got %d", total)
	}
}

// ─── Timezone rollover affects which day a review counts toward ──────────────

func TestGetCounts_TimezoneRollover(t *testing.T) {
	c := setupIntegration(t)
	ctx := context.Background()

	// Tokyo, rollover at 04:00 local. Choose "now" so that local time is
	// 02:00 JST — before rollover, so today's study-day actually started
	// yesterday at 04:00 JST (= yesterday 19:00 UTC).
	cfg := insertDeckConfig(t, c, 20, 1) // rev_per_day = 1
	_, err := c.pool.Exec(ctx, `UPDATE deck_configs SET timezone = 'Asia/Tokyo', rollover_hour = 4 WHERE user_id = $1 AND id = $2`,
		testUserID, cfg)
	if err != nil {
		t.Fatalf("set timezone: %v", err)
	}
	deck := insertDeck(t, c, "deck1", &cfg)
	nt := insertNoteType(t, c)
	note := insertNote(t, c, nt)

	loc, err := time.LoadLocation("Asia/Tokyo")
	if err != nil {
		t.Fatalf("load location: %v", err)
	}
	now := time.Now().In(loc)
	dayStart := DayStart(now, "Asia/Tokyo", 4)

	// A review logged just before today's rollover boundary belongs to the
	// previous study day and must NOT count against today's rev_per_day cap.
	beforeRollover := dayStart.Add(-time.Minute)
	cardA := insertCard(t, c, note, deck, cardFixture{state: StateReview})
	insertReviewLogAt(t, c, cardA, beforeRollover)

	counts, err := c.svc.GetCounts(ctx, testUserID, deck)
	if err != nil {
		t.Fatalf("GetCounts: %v", err)
	}
	if counts.Review != 0 {
		// No due review cards exist yet (cardA isn't due), so Review should
		// be 0 regardless, but the call must not error on the rollover math.
		t.Fatalf("want 0 review remaining (no due cards), got %d", counts.Review)
	}

	// A review logged just after today's rollover boundary DOES count toward
	// today's quota and should consume the rev_per_day=1 cap.
	afterRollover := dayStart.Add(time.Minute)
	insertReviewLogAt(t, c, cardA, afterRollover)

	reviewed, err := c.repo.CountReviewedToday(ctx, testUserID, []string{deck}, dayStart)
	if err != nil {
		t.Fatalf("CountReviewedToday: %v", err)
	}
	if reviewed.Review != 1 {
		t.Fatalf("want 1 review counted after rollover boundary, got %d", reviewed.Review)
	}
}

func insertReviewLogAt(t *testing.T, c integrationComponents, cardID string, ts time.Time) {
	t.Helper()
	_, err := c.pool.Exec(context.Background(), `
		INSERT INTO review_logs (id, user_id, profile_id, card_id, grade, prev_state, prev_ivl, new_ivl, time_ms, ts)
		VALUES ($1, $2, $3, $4, $5, $6, 0, 600, 100, $7)`,
		ids.NewUUID(), testUserID, testProfileID, cardID, int16(Good), int16(StateReview), ts.UTC())
	if err != nil {
		t.Fatalf("insert review_log: %v", err)
	}
}

// ─── Reschedule is idempotent ─────────────────────────────────────────────────

func TestReschedule_Idempotent(t *testing.T) {
	c := setupIntegration(t)
	ctx := context.Background()

	nt := insertNoteType(t, c)
	deck := insertDeck(t, c, "deck1", nil)
	note := insertNote(t, c, nt)
	cardID := insertCard(t, c, note, deck, cardFixture{state: StateReview})

	first, err := c.svc.Reschedule(ctx, testUserID, cardID, ActionSuspend)
	if err != nil {
		t.Fatalf("Reschedule (1st): %v", err)
	}
	if first.Queue != QueueSuspended {
		t.Fatalf("want queue suspended, got %d", first.Queue)
	}

	second, err := c.svc.Reschedule(ctx, testUserID, cardID, ActionSuspend)
	if err != nil {
		t.Fatalf("Reschedule (2nd): %v", err)
	}
	if second.Queue != QueueSuspended {
		t.Fatalf("want queue still suspended after repeat, got %d", second.Queue)
	}
	if second.State != first.State {
		t.Fatalf("want state unchanged across repeated suspend, got %v vs %v", first.State, second.State)
	}
}
