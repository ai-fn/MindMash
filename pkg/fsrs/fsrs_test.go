package fsrs_test

import (
	"testing"
	"time"

	"github.com/mindmesh/mindmesh/pkg/fsrs"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

var now = time.Date(2024, 1, 1, 12, 0, 0, 0, time.UTC)

func defaultEngine() *fsrs.Engine {
	return fsrs.New(fsrs.Params{
		DesiredRetention: 0.9,
		MaxIntervalDays:  36500,
	})
}

// newCard returns a zero-state (New) card with no history.
func newCard() fsrs.SchedulingInput {
	return fsrs.SchedulingInput{State: fsrs.StateNew}
}

func TestGradeNewCard_StateTransitions(t *testing.T) {
	e := defaultEngine()

	tests := []struct {
		grade         fsrs.Grade
		expectState   fsrs.CardState
		expectRepsMin int32
	}{
		{fsrs.Again, fsrs.StateLearning, 1},
		{fsrs.Hard, fsrs.StateLearning, 1},
		{fsrs.Good, fsrs.StateLearning, 1},
		{fsrs.Easy, fsrs.StateReview, 1},
	}
	for _, tc := range tests {
		t.Run("grade_"+gradeStr(tc.grade), func(t *testing.T) {
			res := e.Next(newCard(), tc.grade, now)
			assert.Equal(t, tc.expectState, res.State)
			assert.GreaterOrEqual(t, res.Reps, tc.expectRepsMin)
			assert.Greater(t, res.IntervalSec, int64(0))
			assert.True(t, res.Due.After(now))
		})
	}
}

func TestPreview_ReturnsFourIntervals(t *testing.T) {
	e := defaultEngine()
	prev := e.Preview(newCard(), now)
	require.Greater(t, prev.Again, int64(0))
	require.Greater(t, prev.Hard, int64(0))
	require.Greater(t, prev.Good, int64(0))
	require.Greater(t, prev.Easy, int64(0))
	// Easy should give the longest interval.
	assert.GreaterOrEqual(t, prev.Easy, prev.Good)
}

func TestGradeReviewCard_Good_StabilityIncreases(t *testing.T) {
	e := defaultEngine()

	stab := 10.0
	diff := 5.0
	lastReview := now.Add(-10 * 24 * time.Hour)
	due := now.Add(-1 * time.Hour)
	card := fsrs.SchedulingInput{
		State:      fsrs.StateReview,
		Due:        &due,
		Stability:  &stab,
		Difficulty: &diff,
		Reps:       5,
		Lapses:     0,
		LastReview: &lastReview,
	}
	res := e.Next(card, fsrs.Good, now)
	assert.Equal(t, fsrs.StateReview, res.State)
	assert.Greater(t, res.Stability, stab, "stability should increase on Good from review")
}

func TestGradeReviewCard_Again_LapsesIncrement(t *testing.T) {
	e := defaultEngine()

	stab := 10.0
	diff := 5.0
	lastReview := now.Add(-10 * 24 * time.Hour)
	due := now.Add(-1 * time.Hour)
	card := fsrs.SchedulingInput{
		State:      fsrs.StateReview,
		Due:        &due,
		Stability:  &stab,
		Difficulty: &diff,
		Reps:       5,
		Lapses:     1,
		LastReview: &lastReview,
	}
	res := e.Next(card, fsrs.Again, now)
	assert.Equal(t, int32(2), res.Lapses, "lapses should increment on Again")
	assert.Equal(t, fsrs.StateRelearning, res.State)
}

func TestCustomWeights_Applied(t *testing.T) {
	weights := make([]float64, 19)
	for i := range weights {
		weights[i] = 0.5
	}
	e := fsrs.New(fsrs.Params{
		Weights:          weights,
		DesiredRetention: 0.85,
		MaxIntervalDays:  365,
	})
	res := e.Next(newCard(), fsrs.Good, now)
	assert.Greater(t, res.IntervalSec, int64(0))
}

func TestIntervalSec_MinimumFloor(t *testing.T) {
	e := defaultEngine()
	res := e.Next(newCard(), fsrs.Again, now)
	assert.GreaterOrEqual(t, res.IntervalSec, int64(60))
}

func gradeStr(g fsrs.Grade) string {
	switch g {
	case fsrs.Again:
		return "Again"
	case fsrs.Hard:
		return "Hard"
	case fsrs.Good:
		return "Good"
	case fsrs.Easy:
		return "Easy"
	default:
		return "Unknown"
	}
}

// go-fsrs orders Hard < Good < Easy after clamping, which can push Good and Easy past the cap.
func TestMaxIntervalDays_CapsEveryGrade(t *testing.T) {
	const maxDays = 30
	e := fsrs.New(fsrs.Params{DesiredRetention: 0.9, MaxIntervalDays: maxDays})
	stability, difficulty := 1e6, 1.0
	last := now.AddDate(-1, 0, 0)
	due := now.Add(-time.Hour)
	card := fsrs.SchedulingInput{State: fsrs.StateReview, Due: &due, Stability: &stability, Difficulty: &difficulty, Reps: 50, LastReview: &last}
	limit := int64(maxDays * 24 * 60 * 60)

	p := e.Preview(card, now)
	for _, ivl := range []int64{p.Hard, p.Good, p.Easy} {
		assert.LessOrEqual(t, ivl, limit)
	}
	for _, g := range []fsrs.Grade{fsrs.Hard, fsrs.Good, fsrs.Easy} {
		res := e.Next(card, g, now)
		assert.LessOrEqual(t, res.IntervalSec, limit, gradeStr(g))
		assert.False(t, res.Due.After(now.Add(maxDays*24*time.Hour)), gradeStr(g))
	}
}
