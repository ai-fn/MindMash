// Package fsrs wraps go-fsrs v3 and exposes the scheduling primitives used by
// the Scheduler service. All FSRS state math is confined here; callers only
// deal with domain types.
package fsrs

import (
	"time"

	gofsrs "github.com/open-spaced-repetition/go-fsrs/v3"
)

// Grade maps to the four FSRS ratings. Values are kept stable across the wire
// (proto enum starts at 1 to avoid proto3 zero-value ambiguity).
type Grade int8

const (
	Again Grade = iota + 1 // 1
	Hard                   // 2
	Good                   // 3
	Easy                   // 4
)

// CardState mirrors the card state machine stored in the DB.
type CardState int16

const (
	StateNew        CardState = 0
	StateLearning   CardState = 1
	StateReview     CardState = 2
	StateRelearning CardState = 3
)

// SchedulingInput is the minimum scheduling data needed from a cards row.
type SchedulingInput struct {
	State      CardState
	Due        *time.Time
	Stability  *float64
	Difficulty *float64
	Reps       int32
	Lapses     int32
	LastReview *time.Time
}

// SchedulingResult is what the Scheduler writes back to the cards row after an
// answer. IntervalSec is in seconds (sub-day steps need second-level precision).
type SchedulingResult struct {
	State       CardState
	Due         time.Time
	Stability   float64
	Difficulty  float64
	Reps        int32
	Lapses      int32
	IntervalSec int64
}

// IntervalPreview holds the four next-interval estimates (in seconds) for each
// possible grade, as shown to the user before they answer.
type IntervalPreview struct {
	Again int64
	Hard  int64
	Good  int64
	Easy  int64
}

// Params configures the FSRS engine for a given deck. Build from deck_configs.
type Params struct {
	// Weights is the 19-element FSRS weight vector. If empty or nil the
	// library's DefaultWeights() are used.
	Weights []float64
	// DesiredRetention is the target recall probability (e.g. 0.9).
	DesiredRetention float64
	// MaxIntervalDays caps the longest review interval.
	MaxIntervalDays int
}

// Engine holds a configured FSRS instance.
type Engine struct {
	f *gofsrs.FSRS
}

// New creates an Engine from Params. Safe for concurrent use; the underlying
// FSRS struct is read-only after construction.
func New(p Params) *Engine {
	param := gofsrs.DefaultParam()

	if len(p.Weights) == 19 {
		copy(param.W[:], p.Weights)
	}
	if p.DesiredRetention > 0 {
		param.RequestRetention = p.DesiredRetention
	}
	if p.MaxIntervalDays > 0 {
		param.MaximumInterval = float64(p.MaxIntervalDays)
	}
	// EnableShortTerm lets go-fsrs manage within-day learning steps so we do
	// not have to re-implement the step queue ourselves.
	param.EnableShortTerm = true

	return &Engine{f: gofsrs.NewFSRS(param)}
}

// Next advances a card by one answer and returns the new scheduling state.
// It is the hot-path call inside the answer transaction.
func (e *Engine) Next(card SchedulingInput, grade Grade, now time.Time) SchedulingResult {
	gc := toGoCard(card, now)
	info := e.f.Next(gc, now, toGoRating(grade))
	return fromGoCard(info.Card, now)
}

// Preview returns the four hypothetical next-due intervals without persisting
// anything. Used by GetPreview and returned alongside the Answer response.
func (e *Engine) Preview(card SchedulingInput, now time.Time) IntervalPreview {
	gc := toGoCard(card, now)
	log := e.f.Repeat(gc, now)
	return IntervalPreview{
		Again: intervalSec(log[gofsrs.Again].Card, now),
		Hard:  intervalSec(log[gofsrs.Hard].Card, now),
		Good:  intervalSec(log[gofsrs.Good].Card, now),
		Easy:  intervalSec(log[gofsrs.Easy].Card, now),
	}
}

// toGoCard converts our nullable scheduling columns into a go-fsrs Card.
func toGoCard(c SchedulingInput, now time.Time) gofsrs.Card {
	gc := gofsrs.NewCard()
	gc.State = gofsrs.State(c.State)
	gc.Reps = uint64(c.Reps)
	gc.Lapses = uint64(c.Lapses)
	if c.Stability != nil {
		gc.Stability = *c.Stability
	}
	if c.Difficulty != nil {
		gc.Difficulty = *c.Difficulty
	}
	if c.Due != nil {
		gc.Due = *c.Due
	}
	if c.LastReview != nil {
		gc.LastReview = *c.LastReview
		elapsed := now.Sub(*c.LastReview)
		if elapsed > 0 {
			gc.ElapsedDays = uint64(elapsed.Hours() / 24)
		}
	}
	return gc
}

// fromGoCard maps a go-fsrs Card result back to our domain types.
func fromGoCard(gc gofsrs.Card, now time.Time) SchedulingResult {
	ivl := intervalSec(gc, now)
	return SchedulingResult{
		State:       CardState(gc.State),
		Due:         gc.Due,
		Stability:   gc.Stability,
		Difficulty:  gc.Difficulty,
		Reps:        int32(gc.Reps),
		Lapses:      int32(gc.Lapses),
		IntervalSec: ivl,
	}
}

// intervalSec returns the number of seconds until the card's Due time, measured
// from now. Always >= 60 to avoid zero or negative intervals.
func intervalSec(gc gofsrs.Card, now time.Time) int64 {
	d := gc.Due.Sub(now)
	s := int64(d.Seconds())
	if s < 60 {
		s = 60
	}
	return s
}

// toGoRating converts our Grade to the go-fsrs Rating type.
func toGoRating(g Grade) gofsrs.Rating {
	switch g {
	case Again:
		return gofsrs.Again
	case Hard:
		return gofsrs.Hard
	case Good:
		return gofsrs.Good
	case Easy:
		return gofsrs.Easy
	default:
		return gofsrs.Good
	}
}
