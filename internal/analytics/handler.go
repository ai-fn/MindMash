package analytics

import (
	"context"
	"fmt"
	"strconv"
	"strings"
	"time"

	analyticsv1 "github.com/mindmesh/mindmesh/api/analytics/v1"
	"github.com/mindmesh/mindmesh/internal/platform/auth"
	platformerrors "github.com/mindmesh/mindmesh/internal/platform/errors"
)

// defaultTargetRetention is reported by GetRetention as the comparison baseline.
// Per-deck FSRS desired_retention lives in Scheduler's deck config; looking it
// up there is deferred (see the MVP scope), so the system default stands in.
const defaultTargetRetention = 0.9

// defaultRange is applied when a request's range is empty.
const defaultRange = "30d"

// Handler implements the AnalyticsService gRPC server over the ClickHouse repo.
// Tenancy is always taken from the JWT context, never the request.
type Handler struct {
	analyticsv1.UnimplementedAnalyticsServiceServer
	repo *Repo
	now  func() time.Time // injectable clock for deterministic tests
}

// NewHandler wires the analytics handler.
func NewHandler(repo *Repo) *Handler {
	return &Handler{repo: repo, now: func() time.Time { return time.Now().UTC() }}
}

func (h *Handler) tenant(ctx context.Context) (Tenant, error) {
	id, ok := auth.IdentityFromContext(ctx)
	if !ok {
		return Tenant{}, platformerrors.Unauthenticated("missing identity context")
	}
	return Tenant{UserID: id.UserID}, nil
}

// GetOverview handles GET /v1/stats/overview.
func (h *Handler) GetOverview(ctx context.Context, req *analyticsv1.GetOverviewRequest) (*analyticsv1.GetOverviewResponse, error) {
	t, err := h.tenant(ctx)
	if err != nil {
		return nil, err
	}
	since, err := h.parseRange(req.GetRange())
	if err != nil {
		return nil, err
	}
	loc, err := parseTZ(req.GetTz())
	if err != nil {
		return nil, err
	}

	ov, err := h.repo.Overview(ctx, t, since)
	if err != nil {
		return nil, unavailable(err)
	}
	days, err := h.repo.StudyDays(ctx, t, loc)
	if err != nil {
		return nil, unavailable(err)
	}

	var accuracy float64
	if ov.Reviews > 0 {
		accuracy = float64(ov.Correct) / float64(ov.Reviews)
	}
	return &analyticsv1.GetOverviewResponse{
		Reviews:    ov.Reviews,
		Correct:    ov.Correct,
		Accuracy:   accuracy,
		TimeMs:     ov.TimeMs,
		StreakDays: int32(computeStreak(days, h.today(loc))),
	}, nil
}

// GetHeatmap handles GET /v1/stats/heatmap.
func (h *Handler) GetHeatmap(ctx context.Context, req *analyticsv1.GetHeatmapRequest) (*analyticsv1.GetHeatmapResponse, error) {
	t, err := h.tenant(ctx)
	if err != nil {
		return nil, err
	}
	loc, err := parseTZ(req.GetTz())
	if err != nil {
		return nil, err
	}
	year := int(req.GetYear())
	if year == 0 {
		year = h.now().In(loc).Year()
	}
	start := time.Date(year, 1, 1, 0, 0, 0, 0, loc)
	end := start.AddDate(1, 0, 0)

	days, err := h.repo.Heatmap(ctx, t, loc, start, end)
	if err != nil {
		return nil, unavailable(err)
	}
	resp := &analyticsv1.GetHeatmapResponse{Days: make([]*analyticsv1.HeatmapDay, 0, len(days))}
	for _, d := range days {
		resp.Days = append(resp.Days, &analyticsv1.HeatmapDay{Date: d.Date, Reviews: d.Reviews})
	}
	return resp, nil
}

// GetRetention handles GET /v1/stats/retention.
func (h *Handler) GetRetention(ctx context.Context, req *analyticsv1.GetRetentionRequest) (*analyticsv1.GetRetentionResponse, error) {
	t, err := h.tenant(ctx)
	if err != nil {
		return nil, err
	}
	since, err := h.parseRange(req.GetRange())
	if err != nil {
		return nil, err
	}

	ret, err := h.repo.Retention(ctx, t, req.GetDeck(), since)
	if err != nil {
		return nil, unavailable(err)
	}
	var measured float64
	if ret.DueReviews > 0 {
		measured = float64(ret.DueCorrect) / float64(ret.DueReviews)
	}
	return &analyticsv1.GetRetentionResponse{
		Measured:   measured,
		Target:     defaultTargetRetention,
		DueReviews: ret.DueReviews,
		DueCorrect: ret.DueCorrect,
	}, nil
}

// GetBreakdown handles GET /v1/stats/breakdown.
func (h *Handler) GetBreakdown(ctx context.Context, req *analyticsv1.GetBreakdownRequest) (*analyticsv1.GetBreakdownResponse, error) {
	t, err := h.tenant(ctx)
	if err != nil {
		return nil, err
	}
	by := req.GetBy()
	switch by {
	case "deck", "state", "hour":
	default:
		return nil, platformerrors.InvalidArgument(`by must be one of "deck", "state", "hour"`)
	}
	since, err := h.parseRange(req.GetRange())
	if err != nil {
		return nil, err
	}
	loc, err := parseTZ(req.GetTz())
	if err != nil {
		return nil, err
	}

	buckets, err := h.repo.Breakdown(ctx, t, by, loc, since)
	if err != nil {
		return nil, unavailable(err)
	}
	resp := &analyticsv1.GetBreakdownResponse{Buckets: make([]*analyticsv1.BreakdownBucket, 0, len(buckets))}
	for _, b := range buckets {
		key := b.Key
		if by == "state" {
			key = stateName(b.Key)
		}
		resp.Buckets = append(resp.Buckets, &analyticsv1.BreakdownBucket{
			Key: key, Reviews: b.Reviews, Correct: b.Correct, TimeMs: b.TimeMs,
		})
	}
	return resp, nil
}

// today returns the current date in loc, as UTC midnight like the dates StudyDays returns.
func (h *Handler) today(loc *time.Location) time.Time {
	n := h.now().In(loc)
	return time.Date(n.Year(), n.Month(), n.Day(), 0, 0, 0, 0, time.UTC)
}

// parseTZ resolves an IANA zone name; empty means UTC. "Local" would mean the server's zone.
func parseTZ(name string) (*time.Location, error) {
	if name == "" {
		return time.UTC, nil
	}
	loc, err := time.LoadLocation(name)
	if err != nil || name == "Local" {
		return nil, platformerrors.InvalidArgument(`tz must be an IANA time zone name, e.g. "Europe/Berlin"`)
	}
	return loc, nil
}

// parseRange turns a relative window ("7d", "30d", "all", "") into a lower-bound
// timestamp. Empty defaults to 30d; "all" returns the Unix epoch.
func (h *Handler) parseRange(s string) (time.Time, error) {
	s = strings.TrimSpace(s)
	if s == "" {
		s = defaultRange
	}
	if s == "all" {
		return time.Unix(0, 0).UTC(), nil
	}
	if !strings.HasSuffix(s, "d") {
		return time.Time{}, platformerrors.InvalidArgument(`range must be "<N>d" or "all"`)
	}
	n, err := strconv.Atoi(strings.TrimSuffix(s, "d"))
	if err != nil || n <= 0 {
		return time.Time{}, platformerrors.InvalidArgument(`range must be a positive number of days, e.g. "30d"`)
	}
	return h.now().AddDate(0, 0, -n), nil
}

// unavailable maps a ClickHouse query failure to a 503 — analytics is a
// best-effort mirror, so a backend outage degrades charts rather than erroring
// hard as an internal fault.
func unavailable(err error) error {
	return platformerrors.Unavailable("analytics backend unavailable", err)
}

// computeStreak counts consecutive study days ending at today or yesterday.
// `days` must be distinct UTC dates in descending order. A current streak is
// only credited if the most recent study day is today or yesterday; otherwise
// it has lapsed and the streak is 0.
func computeStreak(days []time.Time, today time.Time) int {
	if len(days) == 0 {
		return 0
	}
	yesterday := today.AddDate(0, 0, -1)
	first := dateOnly(days[0])
	if !first.Equal(today) && !first.Equal(yesterday) {
		return 0
	}
	streak := 1
	expected := first.AddDate(0, 0, -1)
	for _, d := range days[1:] {
		if !dateOnly(d).Equal(expected) {
			break
		}
		streak++
		expected = expected.AddDate(0, 0, -1)
	}
	return streak
}

func dateOnly(t time.Time) time.Time {
	t = t.UTC()
	return time.Date(t.Year(), t.Month(), t.Day(), 0, 0, 0, 0, time.UTC)
}

// stateName maps a card-state ordinal (as a string from ClickHouse) to its name,
// matching Scheduler's CardState constants.
func stateName(ord string) string {
	switch ord {
	case "0":
		return "new"
	case "1":
		return "learning"
	case "2":
		return "review"
	case "3":
		return "relearning"
	default:
		return fmt.Sprintf("state_%s", ord)
	}
}
