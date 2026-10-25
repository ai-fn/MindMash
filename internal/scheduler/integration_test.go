//go:build integration

package scheduler

import (
	"context"
	"math"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/testcontainers/testcontainers-go"
	"github.com/testcontainers/testcontainers-go/modules/postgres"
	"github.com/testcontainers/testcontainers-go/wait"
	"go.uber.org/zap"
	"google.golang.org/grpc"

	collectionv1 "github.com/mindmesh/mindmesh/api/collection/v1"
	"github.com/mindmesh/mindmesh/internal/platform/apperr"
	platformdb "github.com/mindmesh/mindmesh/internal/platform/db"
	"github.com/mindmesh/mindmesh/internal/platform/ids"
)

// stubCollectionClient embeds the interface so unused methods need no stubs.
type stubCollectionClient struct {
	collectionv1.CollectionInternalServiceClient
	calls   int
	missing map[string]bool // ids to leave out, as Collection does for deleted cards
}

func (s *stubCollectionClient) RenderCards(ctx context.Context, in *collectionv1.RenderCardsRequest, opts ...grpc.CallOption) (*collectionv1.RenderCardsResponse, error) {
	s.calls++
	resp := &collectionv1.RenderCardsResponse{}
	for _, id := range in.GetCardIds() {
		if !s.missing[id] {
			resp.Cards = append(resp.Cards, &collectionv1.RenderedCard{CardId: id, FrontHtml: "<div>front:" + id + "</div>"})
		}
	}
	return resp, nil
}

type integrationComponents struct {
	pool *pgxpool.Pool
	repo *PostgresRepository
	svc  *Service
	coll *stubCollectionClient
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

	dsn, err := pgCtr.ConnectionString(ctx, "sslmode=disable")
	if err != nil {
		t.Fatalf("pg connection string: %v", err)
	}
	if err := platformdb.Migrate(ctx, dsn, "../../migrations", "platform", "collection", "scheduler"); err != nil {
		t.Fatalf("run migrations: %v", err)
	}

	pool, err := pgxpool.New(ctx, dsn)
	if err != nil {
		t.Fatalf("pgxpool: %v", err)
	}
	t.Cleanup(pool.Close)

	repo := NewPostgresRepository(pool)
	coll := &stubCollectionClient{missing: map[string]bool{}}
	return integrationComponents{pool: pool, repo: repo, svc: NewService(repo, coll, zap.NewNop()), coll: coll}
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
	queue int16
	due   *time.Time
}

func insertCard(t *testing.T, c integrationComponents, noteID, deckID string, f cardFixture) string {
	t.Helper()
	id := ids.NewUUID()
	_, err := c.pool.Exec(context.Background(), `
		INSERT INTO cards (id, user_id, profile_id, note_id, deck_id, state, queue, due)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8)`,
		id, testUserID, testProfileID, noteID, deckID, int16(f.state), f.queue, f.due)
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

	updated, preview, err := c.svc.Answer(ctx, testUserID, testProfileID, cardID, "", Good, 1500)
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
		if _, _, err := c.svc.Answer(ctx, testUserID, testProfileID, cardID, "", Good, 100); err != nil {
			t.Fatalf("Answer #%d: %v", i, err)
		}
	}
	if n := reviewLogCount(t, c, cardID); n != answers {
		t.Fatalf("want %d review_logs rows, got %d", answers, n)
	}
}

// ─── Daily limits come from today's review_logs ──────────────────────────────

func newCards(t *testing.T, c integrationComponents, deck string, n int) {
	t.Helper()
	nt := insertNoteType(t, c)
	for i := 0; i < n; i++ {
		insertCard(t, c, insertNote(t, c, nt), deck, cardFixture{state: StateNew})
	}
}

func TestBuildQueue_RepeatedFetchKeepsNewLimit(t *testing.T) {
	c := setupIntegration(t)
	ctx := context.Background()
	cfg := insertDeckConfig(t, c, 2, 200)
	deck := insertDeck(t, c, "deck1", &cfg)
	newCards(t, c, deck, 5)

	for i := 0; i < 3; i++ {
		queue, err := c.svc.BuildQueue(ctx, testUserID, testProfileID, deck, 10)
		if err != nil {
			t.Fatalf("BuildQueue #%d: %v", i, err)
		}
		if len(queue) != 2 {
			t.Fatalf("fetch #%d: want 2 new cards (new_per_day=2, nothing answered), got %d", i, len(queue))
		}
	}
}

func TestBuildQueue_NewLimitCountsTodaysAnswers(t *testing.T) {
	c := setupIntegration(t)
	ctx := context.Background()
	cfg := insertDeckConfig(t, c, 2, 200)
	deck := insertDeck(t, c, "deck1", &cfg)
	newCards(t, c, deck, 5)

	queue, err := c.svc.BuildQueue(ctx, testUserID, testProfileID, deck, 10)
	if err != nil {
		t.Fatalf("BuildQueue: %v", err)
	}
	if _, _, err := c.svc.Answer(ctx, testUserID, testProfileID, queue[0].Card.ID, "", Good, 100); err != nil {
		t.Fatalf("Answer: %v", err)
	}

	queue, err = c.svc.BuildQueue(ctx, testUserID, testProfileID, deck, 10)
	if err != nil {
		t.Fatalf("BuildQueue: %v", err)
	}
	var fresh int
	for _, qc := range queue {
		if qc.Card.State == StateNew {
			fresh++
		}
	}
	if fresh != 1 {
		t.Fatalf("want 1 new card left today (2 per day, 1 answered), got %d", fresh)
	}
}

func TestBuildQueue_LearningNotCappedByReviewLimit(t *testing.T) {
	c := setupIntegration(t)
	ctx := context.Background()
	cfg := insertDeckConfig(t, c, 0, 1)
	deck := insertDeck(t, c, "deck1", &cfg)
	nt := insertNoteType(t, c)
	past := time.Now().Add(-time.Hour)
	for _, st := range []CardState{StateReview, StateReview, StateLearning, StateRelearning} {
		insertCard(t, c, insertNote(t, c, nt), deck, cardFixture{state: st, due: &past})
	}

	queue, err := c.svc.BuildQueue(ctx, testUserID, testProfileID, deck, 10)
	if err != nil {
		t.Fatalf("BuildQueue: %v", err)
	}
	var reviews, learning int
	for _, qc := range queue {
		if qc.Card.State == StateReview {
			reviews++
		} else {
			learning++
		}
	}
	if reviews != 1 || learning != 2 {
		t.Fatalf("want 1 review (rev_per_day=1) and both learning cards, got %d reviews, %d learning", reviews, learning)
	}
}

func TestCountReviewedToday_UsesDeckAtReviewTime(t *testing.T) {
	c := setupIntegration(t)
	ctx := context.Background()
	deckA := insertDeck(t, c, "a", nil)
	deckB := insertDeck(t, c, "b", nil)
	nt := insertNoteType(t, c)
	cardID := insertCard(t, c, insertNote(t, c, nt), deckA, cardFixture{state: StateNew})

	if _, _, err := c.svc.Answer(ctx, testUserID, testProfileID, cardID, "", Good, 100); err != nil {
		t.Fatalf("Answer: %v", err)
	}
	if _, err := c.pool.Exec(ctx, `UPDATE cards SET deck_id = $3 WHERE user_id = $1 AND id = $2`, testUserID, cardID, deckB); err != nil {
		t.Fatalf("move card: %v", err)
	}

	dayStart := DayStart(time.Now(), "UTC", 4)
	for deck, want := range map[string]int32{deckA: 1, deckB: 0} {
		got, err := c.repo.CountReviewedToday(ctx, testUserID, []string{deck}, dayStart)
		if err != nil {
			t.Fatalf("CountReviewedToday: %v", err)
		}
		if got.New != want {
			t.Fatalf("deck %s: want %d new reviewed today, got %d", deck, want, got.New)
		}
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
	insertReviewLogAt(t, c, cardA, deck, beforeRollover)

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
	insertReviewLogAt(t, c, cardA, deck, afterRollover)

	reviewed, err := c.repo.CountReviewedToday(ctx, testUserID, []string{deck}, dayStart)
	if err != nil {
		t.Fatalf("CountReviewedToday: %v", err)
	}
	if reviewed.Review != 1 {
		t.Fatalf("want 1 review counted after rollover boundary, got %d", reviewed.Review)
	}
}

func insertReviewLogAt(t *testing.T, c integrationComponents, cardID, deckID string, ts time.Time) {
	t.Helper()
	_, err := c.pool.Exec(context.Background(), `
		INSERT INTO review_logs (id, user_id, profile_id, card_id, deck_id, grade, prev_state, prev_ivl, new_ivl, time_ms, ts)
		VALUES ($1, $2, $3, $4, $5, $6, $7, 0, 600, 100, $8)`,
		ids.NewUUID(), testUserID, testProfileID, cardID, deckID, int16(Good), int16(StateReview), ts.UTC())
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

// ─── Answer guards ────────────────────────────────────────────────────────────

func singleCard(t *testing.T, c integrationComponents, f cardFixture) (deckID, cardID string) {
	t.Helper()
	deckID = insertDeck(t, c, "deck1", nil)
	return deckID, insertCard(t, c, insertNote(t, c, insertNoteType(t, c)), deckID, f)
}

func cardQueue(t *testing.T, c integrationComponents, cardID string) int16 {
	t.Helper()
	var q int16
	if err := c.pool.QueryRow(context.Background(),
		`SELECT queue FROM cards WHERE user_id = $1 AND id = $2`, testUserID, cardID).Scan(&q); err != nil {
		t.Fatalf("read card queue: %v", err)
	}
	return q
}

func TestAnswer_RejectsSuspendedCard(t *testing.T) {
	c := setupIntegration(t)
	_, cardID := singleCard(t, c, cardFixture{state: StateNew, queue: QueueSuspended})

	_, _, err := c.svc.Answer(context.Background(), testUserID, testProfileID, cardID, "", Good, 100)
	if !apperr.IsFailedPrecondition(err) {
		t.Fatalf("want FailedPrecondition, got %v", err)
	}
	if n := reviewLogCount(t, c, cardID); n != 0 {
		t.Fatalf("want no review_logs rows, got %d", n)
	}
}

func TestAnswer_RejectsCardBuriedToday(t *testing.T) {
	c := setupIntegration(t)
	ctx := context.Background()
	_, cardID := singleCard(t, c, cardFixture{state: StateNew})
	if _, err := c.svc.Reschedule(ctx, testUserID, cardID, ActionBury); err != nil {
		t.Fatalf("Reschedule: %v", err)
	}

	_, _, err := c.svc.Answer(ctx, testUserID, testProfileID, cardID, "", Good, 100)
	if !apperr.IsFailedPrecondition(err) {
		t.Fatalf("want FailedPrecondition, got %v", err)
	}
}

func TestAnswer_UnburiesCardFromPreviousDay(t *testing.T) {
	c := setupIntegration(t)
	_, cardID := singleCard(t, c, cardFixture{state: StateNew, queue: QueueBuried})
	buryAt(t, c, cardID, time.Now().Add(-48*time.Hour))

	updated, _, err := c.svc.Answer(context.Background(), testUserID, testProfileID, cardID, "", Good, 100)
	if err != nil {
		t.Fatalf("Answer: %v", err)
	}
	if updated.Queue < 0 {
		t.Fatalf("want card unburied, queue %d", updated.Queue)
	}
}

func buryAt(t *testing.T, c integrationComponents, cardID string, at time.Time) {
	t.Helper()
	if _, err := c.pool.Exec(context.Background(),
		`UPDATE cards SET queue = $3, mtime = $4 WHERE user_id = $1 AND id = $2`,
		testUserID, cardID, QueueBuried, at.UTC()); err != nil {
		t.Fatalf("bury card: %v", err)
	}
}

func TestAnswer_ClientReviewIDIsIdempotent(t *testing.T) {
	c := setupIntegration(t)
	ctx := context.Background()
	_, cardID := singleCard(t, c, cardFixture{state: StateNew})
	key := ids.NewUUID()

	first, _, err := c.svc.Answer(ctx, testUserID, testProfileID, cardID, key, Good, 100)
	if err != nil {
		t.Fatalf("Answer (1st): %v", err)
	}
	second, _, err := c.svc.Answer(ctx, testUserID, testProfileID, cardID, key, Easy, 100)
	if err != nil {
		t.Fatalf("Answer (retry): %v", err)
	}
	if n := reviewLogCount(t, c, cardID); n != 1 {
		t.Fatalf("want 1 review_logs row after retry, got %d", n)
	}
	if second.Reps != first.Reps || second.State != first.State || !second.Due.Equal(*first.Due) {
		t.Fatalf("want retry to return the stored state %+v, got %+v", first, second)
	}

	if _, err := c.svc.Reschedule(ctx, testUserID, cardID, ActionSuspend); err != nil {
		t.Fatalf("Reschedule: %v", err)
	}
	if _, _, err := c.svc.Answer(ctx, testUserID, testProfileID, cardID, key, Good, 100); err != nil {
		t.Fatalf("want a retry after suspend to return the stored state, got %v", err)
	}

	_, other := singleCard(t, c, cardFixture{state: StateNew})
	if _, _, err := c.svc.Answer(ctx, testUserID, testProfileID, other, key, Good, 100); !apperr.IsInvalidArgument(err) {
		t.Fatalf("want InvalidArgument for a key reused on another card, got %v", err)
	}
}

func TestAnswer_ClampsTimeMs(t *testing.T) {
	c := setupIntegration(t)
	ctx := context.Background()
	for _, tt := range []struct{ in, want int32 }{{-5, 0}, {math.MaxInt32, maxAnswerTimeMs}} {
		_, cardID := singleCard(t, c, cardFixture{state: StateNew})
		if _, _, err := c.svc.Answer(ctx, testUserID, testProfileID, cardID, "", Good, tt.in); err != nil {
			t.Fatalf("Answer: %v", err)
		}
		var got int32
		if err := c.pool.QueryRow(ctx, `SELECT time_ms FROM review_logs WHERE user_id = $1 AND card_id = $2`,
			testUserID, cardID).Scan(&got); err != nil {
			t.Fatalf("read time_ms: %v", err)
		}
		if got != tt.want {
			t.Fatalf("time_ms %d stored as %d, want %d", tt.in, got, tt.want)
		}
	}
}

// Intervals near the 36500-day FSRS cap exceed int32 seconds.
func TestAnswer_StoresLongInterval(t *testing.T) {
	c := setupIntegration(t)
	ctx := context.Background()
	due := time.Now().Add(-time.Hour)
	_, cardID := singleCard(t, c, cardFixture{state: StateReview, due: &due})
	if _, err := c.pool.Exec(ctx, `
		UPDATE cards SET stability = 1e6, difficulty = 1, reps = 50, last_review = $3
		WHERE user_id = $1 AND id = $2`, testUserID, cardID, due.AddDate(-50, 0, 0)); err != nil {
		t.Fatalf("set stability: %v", err)
	}

	if _, _, err := c.svc.Answer(ctx, testUserID, testProfileID, cardID, "", Easy, 100); err != nil {
		t.Fatalf("Answer: %v", err)
	}
	var newIvl int64
	if err := c.pool.QueryRow(ctx, `SELECT new_ivl FROM review_logs WHERE user_id = $1 AND card_id = $2`,
		testUserID, cardID).Scan(&newIvl); err != nil {
		t.Fatalf("read new_ivl: %v", err)
	}
	if newIvl <= math.MaxInt32 {
		t.Fatalf("want new_ivl beyond int32, got %d", newIvl)
	}
}

func TestAnswer_CapsIntervalAtDeckMax(t *testing.T) {
	c := setupIntegration(t)
	ctx := context.Background()
	cfgID := insertDeckConfig(t, c, 20, 200)
	if _, err := c.pool.Exec(ctx, `UPDATE deck_configs SET max_interval_days = 30 WHERE user_id = $1 AND id = $2`,
		testUserID, cfgID); err != nil {
		t.Fatalf("set max_interval_days: %v", err)
	}
	due := time.Now().Add(-time.Hour)
	deckID := insertDeck(t, c, "Capped", &cfgID)
	cardID := insertCard(t, c, insertNote(t, c, insertNoteType(t, c)), deckID, cardFixture{state: StateReview, due: &due})
	if _, err := c.pool.Exec(ctx, `
		UPDATE cards SET stability = 1e6, difficulty = 1, reps = 50, last_review = $3
		WHERE user_id = $1 AND id = $2`, testUserID, cardID, due.AddDate(-1, 0, 0)); err != nil {
		t.Fatalf("set stability: %v", err)
	}

	if _, _, err := c.svc.Answer(ctx, testUserID, testProfileID, cardID, "", Easy, 100); err != nil {
		t.Fatalf("Answer: %v", err)
	}
	var newIvl int64
	if err := c.pool.QueryRow(ctx, `SELECT new_ivl FROM review_logs WHERE user_id = $1 AND card_id = $2`,
		testUserID, cardID).Scan(&newIvl); err != nil {
		t.Fatalf("read new_ivl: %v", err)
	}
	if newIvl > 30*86400 {
		t.Fatalf("new_ivl = %d s, want at most the deck's 30 days", newIvl)
	}
}

// ─── Buried and suspended cards in the queue ──────────────────────────────────

func TestBuildQueue_SkipsSuspendedNewCards(t *testing.T) {
	c := setupIntegration(t)
	deck, _ := singleCard(t, c, cardFixture{state: StateNew, queue: QueueSuspended})

	queue, err := c.svc.BuildQueue(context.Background(), testUserID, testProfileID, deck, 10)
	if err != nil {
		t.Fatalf("BuildQueue: %v", err)
	}
	if len(queue) != 0 {
		t.Fatalf("want suspended new card excluded, got %d cards", len(queue))
	}
}

func TestBuildQueue_UnburiesCardsFromPreviousDay(t *testing.T) {
	c := setupIntegration(t)
	ctx := context.Background()
	deck, old := singleCard(t, c, cardFixture{state: StateNew, queue: QueueBuried})
	buryAt(t, c, old, time.Now().Add(-48*time.Hour))
	today := insertCard(t, c, insertNote(t, c, insertNoteType(t, c)), deck, cardFixture{state: StateNew})
	if _, err := c.svc.Reschedule(ctx, testUserID, today, ActionBury); err != nil {
		t.Fatalf("Reschedule: %v", err)
	}

	counts, err := c.svc.GetCounts(ctx, testUserID, deck)
	if err != nil {
		t.Fatalf("GetCounts: %v", err)
	}
	if counts.New != 1 {
		t.Fatalf("want 1 new card counted after unbury, got %d", counts.New)
	}
	queue, err := c.svc.BuildQueue(ctx, testUserID, testProfileID, deck, 10)
	if err != nil {
		t.Fatalf("BuildQueue: %v", err)
	}
	if len(queue) != 1 || queue[0].Card.ID != old {
		t.Fatalf("want only the card buried on a previous day, got %+v", queue)
	}
	if q := cardQueue(t, c, old); q != 0 {
		t.Fatalf("want unburied card stored with queue 0, got %d", q)
	}
	if q := cardQueue(t, c, today); q != QueueBuried {
		t.Fatalf("want card buried today to stay buried, got %d", q)
	}
}

// ─── Rendering, counts and preview ────────────────────────────────────────────

func TestBuildQueue_RendersInOneBatchAndSkipsMissing(t *testing.T) {
	c := setupIntegration(t)
	deck := insertDeck(t, c, "deck1", nil)
	nt := insertNoteType(t, c)
	var ids []string
	for range 3 {
		ids = append(ids, insertCard(t, c, insertNote(t, c, nt), deck, cardFixture{state: StateNew}))
	}
	c.coll.missing[ids[1]] = true

	queue, err := c.svc.BuildQueue(context.Background(), testUserID, testProfileID, deck, 10)
	if err != nil {
		t.Fatalf("BuildQueue: %v", err)
	}
	if c.coll.calls != 1 {
		t.Fatalf("want 1 render call, got %d", c.coll.calls)
	}
	if len(queue) != 2 || queue[0].Card.ID != ids[0] || queue[1].Card.ID != ids[2] {
		t.Fatalf("want cards %s and %s in order, got %+v", ids[0], ids[2], queue)
	}
	if queue[1].FrontHTML != "<div>front:"+ids[2]+"</div>" {
		t.Fatalf("front html not matched to its card: %q", queue[1].FrontHTML)
	}
}

func TestBuildQueue_RendersInChunks(t *testing.T) {
	c := setupIntegration(t)
	cfg := insertDeckConfig(t, c, 100, 200)
	deck := insertDeck(t, c, "deck1", &cfg)
	newCards(t, c, deck, renderBatch+5)

	queue, err := c.svc.BuildQueue(context.Background(), testUserID, testProfileID, deck, 100)
	if err != nil {
		t.Fatalf("BuildQueue: %v", err)
	}
	if len(queue) != renderBatch+5 || c.coll.calls != 2 {
		t.Fatalf("want %d cards in 2 render calls, got %d cards in %d calls", renderBatch+5, len(queue), c.coll.calls)
	}
}

func TestGetCounts_CountsBeyondOldCap(t *testing.T) {
	c := setupIntegration(t)
	ctx := context.Background()
	cfg := insertDeckConfig(t, c, 20000, 200)
	deck := insertDeck(t, c, "deck1", &cfg)
	note := insertNote(t, c, insertNoteType(t, c))
	if _, err := c.pool.Exec(ctx, `
		INSERT INTO cards (id, user_id, profile_id, note_id, deck_id, state)
		SELECT gen_random_uuid(), $1, $2, $3, $4, 0 FROM generate_series(1, 10001)`,
		testUserID, testProfileID, note, deck); err != nil {
		t.Fatalf("insert cards: %v", err)
	}

	counts, err := c.svc.GetCounts(ctx, testUserID, deck)
	if err != nil {
		t.Fatalf("GetCounts: %v", err)
	}
	if counts.New != 10001 {
		t.Fatalf("want 10001 new, got %d", counts.New)
	}
}

func TestGetCounts_NegativeLimitDoesNotWrap(t *testing.T) {
	c := setupIntegration(t)
	ctx := context.Background()
	cfg := insertDeckConfig(t, c, math.MinInt32, 200)
	deck := insertDeck(t, c, "deck1", &cfg)
	nt := insertNoteType(t, c)
	cardID := insertCard(t, c, insertNote(t, c, nt), deck, cardFixture{state: StateNew})
	insertCard(t, c, insertNote(t, c, nt), deck, cardFixture{state: StateNew})
	if _, err := c.pool.Exec(ctx, `
		INSERT INTO review_logs (id, user_id, profile_id, card_id, deck_id, grade, prev_state, new_ivl)
		VALUES ($1, $2, $3, $4, $5, 3, 0, 600)`,
		ids.NewUUID(), testUserID, testProfileID, cardID, deck); err != nil {
		t.Fatalf("insert review_log: %v", err)
	}

	counts, err := c.svc.GetCounts(ctx, testUserID, deck)
	if err != nil {
		t.Fatalf("GetCounts: %v", err)
	}
	if counts.New != 0 {
		t.Fatalf("want 0 new under a negative limit, got %d", counts.New)
	}
}

func TestPreview_DoesNotLockCard(t *testing.T) {
	c := setupIntegration(t)
	ctx := context.Background()
	_, cardID := singleCard(t, c, cardFixture{state: StateNew})

	tx, err := c.pool.Begin(ctx)
	if err != nil {
		t.Fatalf("begin: %v", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if _, err := tx.Exec(ctx, `SELECT 1 FROM cards WHERE user_id = $1 AND id = $2 FOR UPDATE`, testUserID, cardID); err != nil {
		t.Fatalf("lock card: %v", err)
	}

	pctx, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()
	if _, err := c.svc.Preview(pctx, testUserID, cardID); err != nil {
		t.Fatalf("Preview blocked behind a locked card: %v", err)
	}
}
