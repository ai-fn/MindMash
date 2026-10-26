//go:build integration

package analytics

import (
	"context"
	"encoding/json"
	"slices"
	"testing"
	"time"

	chdriver "github.com/ClickHouse/clickhouse-go/v2/lib/driver"
	chmod "github.com/testcontainers/testcontainers-go/modules/clickhouse"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	analyticsv1 "github.com/mindmesh/mindmesh/api/analytics/v1"
	"github.com/mindmesh/mindmesh/internal/platform/auth"
	"github.com/mindmesh/mindmesh/internal/platform/clickhouse"
	"github.com/mindmesh/mindmesh/internal/platform/events"
	"go.uber.org/zap"
)

const (
	userA, profA = "user-a", "prof-a"
	userB, profB = "user-b", "prof-b"
	deckSpanish  = "deck-spanish"
	deckFrench   = "deck-french"
)

// setupClickHouseConn starts ClickHouse with the analytics schema. Tests call HandleBatch directly;
// the Kafka transport is covered by the events tests.
func setupClickHouseConn(t *testing.T) (chdriver.Conn, context.Context) {
	t.Helper()
	ctx := context.Background()

	ctr, err := chmod.Run(ctx, "clickhouse/clickhouse-server:25.8-alpine",
		chmod.WithUsername("mindmesh"),
		chmod.WithPassword("mindmesh"),
		chmod.WithDatabase("mindmesh"),
	)
	if err != nil {
		t.Fatalf("start clickhouse: %v", err)
	}
	t.Cleanup(func() { _ = ctr.Terminate(context.Background()) })

	host, err := ctr.ConnectionHost(ctx)
	if err != nil {
		t.Fatalf("connection host: %v", err)
	}
	conn, err := clickhouse.New(ctx, clickhouse.Config{
		Addr: host, Database: "mindmesh", Username: "mindmesh", Password: "mindmesh",
	})
	if err != nil {
		t.Fatalf("connect clickhouse: %v", err)
	}
	t.Cleanup(func() { _ = conn.Close() })

	if err := clickhouse.RunMigrations(ctx, conn, "../../migrations/clickhouse"); err != nil {
		t.Fatalf("run migrations: %v", err)
	}
	return conn, ctx
}

func TestAnalytics_IngestAggregateIsolation(t *testing.T) {
	d, ctx := setupClickHouseConn(t)
	agg := NewAggregator(d, zap.NewNop())
	h := NewHandler(NewRepo(d))

	now := time.Now().UTC()
	// fixed clock so streak/range math is deterministic relative to the events.
	h.now = func() time.Time { return now }
	today := now.Truncate(24 * time.Hour)

	feed(t, ctx, agg,
		// userA: 3 reviews today across two decks, mixed grades & states.
		msgOf(t, ev{"a1", userA, profA, "c1", deckSpanish, 3, 2, 2, 1500, today}), // review, correct
		msgOf(t, ev{"a2", userA, profA, "c2", deckSpanish, 1, 2, 3, 800, today}),  // review, lapse (grade Again)
		// Dropped without failing the batch: undecodable, and another event type.
		events.Message{Topic: ReviewTopic, Value: []byte("{"), Headers: map[string]string{"event_type": eventReviewRecorded}},
		events.Message{Topic: ReviewTopic, Value: []byte("{}"), Headers: map[string]string{"event_type": "review.undone"}},
		msgOf(t, ev{"a3", userA, profA, "c3", deckFrench, 4, 0, 1, 1200, today}), // new card, correct
		// userA: 2 reviews yesterday (to make a 2-day streak).
		msgOf(t, ev{"a4", userA, profA, "c1", deckSpanish, 3, 2, 2, 1000, today.AddDate(0, 0, -1)}),
		// Reviewed under another of userA's profiles: stats are per user, not per profile.
		msgOf(t, ev{"a5", userA, "prof-a2", "c4", deckFrench, 2, 2, 2, 900, today.AddDate(0, 0, -1)}),
		// userB: 1 review today — must never leak into userA's stats.
		msgOf(t, ev{"b1", userB, profB, "c9", "deck-b", 3, 2, 2, 500, today}),
	)
	// Idempotency: redeliver a1 — ReplacingMergeTree + FINAL must fold it.
	feed(t, ctx, agg, msgOf(t, ev{"a1", userA, profA, "c1", deckSpanish, 3, 2, 2, 1500, today}))
	// A batch of nothing to insert is a no-op, not an error.
	feed(t, ctx, agg, events.Message{Topic: ReviewTopic, Value: []byte("{}"), Headers: map[string]string{"event_type": "review.undone"}})

	ctxA := auth.WithIdentity(ctx, auth.IdentityContext{UserID: userA, ProfileID: profA})

	// Overview (all-time).
	ov, err := h.GetOverview(ctxA, &analyticsv1.GetOverviewRequest{Range: "all"})
	if err != nil {
		t.Fatalf("overview: %v", err)
	}
	if ov.Reviews != 5 { // a1..a5, dedup of the redelivered a1
		t.Errorf("reviews = %d, want 5 (dedup)", ov.Reviews)
	}
	if ov.Correct != 4 { // a2 is Again
		t.Errorf("correct = %d, want 4", ov.Correct)
	}
	if ov.TimeMs != 1500+800+1200+1000+900 {
		t.Errorf("time_ms = %d, want 5400", ov.TimeMs)
	}
	if ov.StreakDays != 2 {
		t.Errorf("streak = %d, want 2", ov.StreakDays)
	}
	if ov.Accuracy < 0.79 || ov.Accuracy > 0.81 {
		t.Errorf("accuracy = %f, want ~0.8", ov.Accuracy)
	}

	// Retention: due reviews are prev_state==2 (a1,a2,a4 for userA all-time, +b1 excluded).
	ret, err := h.GetRetention(ctxA, &analyticsv1.GetRetentionRequest{Range: "all"})
	if err != nil {
		t.Fatalf("retention: %v", err)
	}
	// due (prev_state==2): a1,a2,a4,a5; correct (grade>1): a1,a4,a5 — a2 is Again.
	if ret.DueReviews != 4 || ret.DueCorrect != 3 {
		t.Errorf("retention due=%d correct=%d, want 4/3", ret.DueReviews, ret.DueCorrect)
	}
	if ret.Target != defaultTargetRetention {
		t.Errorf("target = %f, want %f", ret.Target, defaultTargetRetention)
	}

	// Retention scoped to one deck.
	retSp, err := h.GetRetention(ctxA, &analyticsv1.GetRetentionRequest{Range: "all", Deck: deckSpanish})
	if err != nil {
		t.Fatalf("retention spanish: %v", err)
	}
	if retSp.DueReviews != 3 { // a1,a2,a4 all spanish + review-state
		t.Errorf("spanish due = %d, want 3", retSp.DueReviews)
	}

	// Breakdown by deck.
	bd, err := h.GetBreakdown(ctxA, &analyticsv1.GetBreakdownRequest{By: "deck", Range: "all"})
	if err != nil {
		t.Fatalf("breakdown deck: %v", err)
	}
	byKey := map[string]int64{}
	for _, b := range bd.Buckets {
		byKey[b.Key] = b.Reviews
	}
	if byKey[deckSpanish] != 3 || byKey[deckFrench] != 2 {
		t.Errorf("deck breakdown = %v, want spanish:3 french:2", byKey)
	}

	// Breakdown by state maps ordinals to names.
	bs, err := h.GetBreakdown(ctxA, &analyticsv1.GetBreakdownRequest{By: "state", Range: "all"})
	if err != nil {
		t.Fatalf("breakdown state: %v", err)
	}
	stateKeys := map[string]int64{}
	for _, b := range bs.Buckets {
		stateKeys[b.Key] = b.Reviews
	}
	if stateKeys["new"] != 1 || stateKeys["review"] != 4 { // a3 new; a1,a2,a4,a5 review
		t.Errorf("state breakdown = %v, want new:1 review:4", stateKeys)
	}

	// Heatmap for the current year includes today and yesterday.
	hm, err := h.GetHeatmap(ctxA, &analyticsv1.GetHeatmapRequest{Year: int32(now.Year())})
	if err != nil {
		t.Fatalf("heatmap: %v", err)
	}
	var total int64
	for _, day := range hm.Days {
		total += day.Reviews
	}
	if total != 5 {
		t.Errorf("heatmap total = %d, want 5", total)
	}

	ovA2, err := h.GetOverview(auth.WithIdentity(ctx, auth.IdentityContext{UserID: userA, ProfileID: "prof-a2"}),
		&analyticsv1.GetOverviewRequest{Range: "all"})
	if err != nil {
		t.Fatalf("overview A, other profile: %v", err)
	}
	if ovA2.Reviews != 5 {
		t.Errorf("userA reviews from another profile = %d, want 5", ovA2.Reviews)
	}

	// userB sees only their own single review.
	ctxB := auth.WithIdentity(ctx, auth.IdentityContext{UserID: userB, ProfileID: profB})
	ovB, err := h.GetOverview(ctxB, &analyticsv1.GetOverviewRequest{Range: "all"})
	if err != nil {
		t.Fatalf("overview B: %v", err)
	}
	if ovB.Reviews != 1 {
		t.Errorf("userB reviews = %d, want 1 (isolation)", ovB.Reviews)
	}
}

// ev is a compact review-event spec for the test.
type ev struct {
	id, user, prof, card, deck string
	grade, prevState, newState int
	timeMs                     int
	ts                         time.Time
}

// feed drives one batch through the aggregator exactly as the Kafka consumer would.
func feed(t *testing.T, ctx context.Context, agg *Aggregator, msgs ...events.Message) {
	t.Helper()
	if err := agg.HandleBatch(ctx, msgs); err != nil {
		t.Fatalf("aggregator handle batch: %v", err)
	}
}

func msgOf(t *testing.T, e ev) events.Message {
	t.Helper()
	payload := reviewPayload{
		ID: e.id, UserID: e.user, ProfileID: e.prof, CardID: e.card, DeckID: e.deck,
		Grade: int8(e.grade), PrevState: int16(e.prevState), NewState: int16(e.newState),
		TimeMs: int32(e.timeMs), Ts: e.ts,
	}
	b, err := json.Marshal(payload)
	if err != nil {
		t.Fatalf("marshal event: %v", err)
	}
	return events.Message{
		Topic:   ReviewTopic,
		Value:   b,
		Headers: map[string]string{"event_type": eventReviewRecorded},
	}
}

func TestAnalytics_DaysAndHoursInCallerTimeZone(t *testing.T) {
	d, ctx := setupClickHouseConn(t)
	agg := NewAggregator(d, zap.NewNop())
	h := NewHandler(NewRepo(d))
	h.now = func() time.Time { return time.Date(2026, 3, 11, 1, 0, 0, 0, time.UTC) }

	// Two UTC days, but both on 2026-03-11 in Tokyo (UTC+9).
	feed(t, ctx, agg,
		msgOf(t, ev{"tz1", userA, profA, "c1", deckSpanish, 3, 2, 2, 100, time.Date(2026, 3, 10, 23, 30, 0, 0, time.UTC)}),
		msgOf(t, ev{"tz2", userA, profA, "c2", deckSpanish, 3, 2, 2, 100, time.Date(2026, 3, 11, 0, 30, 0, 0, time.UTC)}),
	)
	ctxA := auth.WithIdentity(ctx, auth.IdentityContext{UserID: userA, ProfileID: profA})

	for _, tc := range []struct {
		tz     string
		streak int32
		dates  []string
		hours  []string
	}{
		{"", 2, []string{"2026-03-10", "2026-03-11"}, []string{"0", "23"}},
		{"Asia/Tokyo", 1, []string{"2026-03-11"}, []string{"8", "9"}},
	} {
		t.Run("tz="+tc.tz, func(t *testing.T) {
			ov, err := h.GetOverview(ctxA, &analyticsv1.GetOverviewRequest{Range: "all", Tz: tc.tz})
			if err != nil {
				t.Fatal(err)
			}
			if ov.StreakDays != tc.streak {
				t.Errorf("streak = %d, want %d", ov.StreakDays, tc.streak)
			}

			hm, err := h.GetHeatmap(ctxA, &analyticsv1.GetHeatmapRequest{Year: 2026, Tz: tc.tz})
			if err != nil {
				t.Fatal(err)
			}
			var dates []string
			for _, day := range hm.Days {
				dates = append(dates, day.Date)
			}
			if !slices.Equal(dates, tc.dates) {
				t.Errorf("heatmap dates = %v, want %v", dates, tc.dates)
			}

			br, err := h.GetBreakdown(ctxA, &analyticsv1.GetBreakdownRequest{By: "hour", Range: "all", Tz: tc.tz})
			if err != nil {
				t.Fatal(err)
			}
			var hours []string
			for _, b := range br.Buckets {
				hours = append(hours, b.Key)
			}
			slices.Sort(hours)
			if !slices.Equal(hours, tc.hours) {
				t.Errorf("hours = %v, want %v", hours, tc.hours)
			}
		})
	}

	_, err := h.GetOverview(ctxA, &analyticsv1.GetOverviewRequest{Tz: "Mars/Olympus"})
	if status.Code(err) != codes.InvalidArgument {
		t.Errorf("unknown tz: code = %v, want InvalidArgument", status.Code(err))
	}
}
