package analytics

import (
	"testing"
	"time"
)

func TestParseRange(t *testing.T) {
	now := time.Date(2026, 6, 24, 15, 0, 0, 0, time.UTC)
	h := &Handler{now: func() time.Time { return now }}

	tests := []struct {
		in      string
		want    time.Time
		wantErr bool
	}{
		{"", now.AddDate(0, 0, -30), false},        // default 30d
		{"7d", now.AddDate(0, 0, -7), false},       //
		{"365d", now.AddDate(0, 0, -365), false},   //
		{"all", time.Unix(0, 0).UTC(), false},      //
		{"  30d  ", now.AddDate(0, 0, -30), false}, // trimmed
		{"30", time.Time{}, true},                  // missing suffix
		{"xd", time.Time{}, true},                  // non-numeric
		{"0d", time.Time{}, true},                  // non-positive
		{"-5d", time.Time{}, true},                 // negative
	}
	for _, tc := range tests {
		got, err := h.parseRange(tc.in)
		if tc.wantErr {
			if err == nil {
				t.Errorf("parseRange(%q): expected error, got %v", tc.in, got)
			}
			continue
		}
		if err != nil {
			t.Errorf("parseRange(%q): unexpected error: %v", tc.in, err)
			continue
		}
		if !got.Equal(tc.want) {
			t.Errorf("parseRange(%q) = %v, want %v", tc.in, got, tc.want)
		}
	}
}

func TestComputeStreak(t *testing.T) {
	today := time.Date(2026, 6, 24, 0, 0, 0, 0, time.UTC)
	day := func(offset int) time.Time { return today.AddDate(0, 0, offset) }

	tests := []struct {
		name string
		days []time.Time
		want int
	}{
		{"empty", nil, 0},
		{"today only", []time.Time{day(0)}, 1},
		{"yesterday only (still current)", []time.Time{day(-1)}, 1},
		{"lapsed (two days ago)", []time.Time{day(-2)}, 0},
		{"three consecutive ending today", []time.Time{day(0), day(-1), day(-2)}, 3},
		{"consecutive ending yesterday", []time.Time{day(-1), day(-2), day(-3)}, 3},
		{"gap breaks streak", []time.Time{day(0), day(-1), day(-3)}, 2},
		{"future day ignored as start, today continues", []time.Time{day(0), day(-1)}, 2},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := computeStreak(tc.days, today); got != tc.want {
				t.Errorf("computeStreak(%v) = %d, want %d", tc.days, got, tc.want)
			}
		})
	}
}

func TestStateName(t *testing.T) {
	cases := map[string]string{
		"0": "new", "1": "learning", "2": "review", "3": "relearning", "9": "state_9",
	}
	for in, want := range cases {
		if got := stateName(in); got != want {
			t.Errorf("stateName(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestParseTZ(t *testing.T) {
	for _, tc := range []struct {
		in, want string
		ok       bool
	}{
		{"", "UTC", true},
		{"Europe/Berlin", "Europe/Berlin", true},
		{"Local", "", false}, // the server's zone, not the caller's
		{"Mars/Olympus", "", false},
	} {
		loc, err := parseTZ(tc.in)
		if (err == nil) != tc.ok {
			t.Errorf("parseTZ(%q) err = %v, want ok=%v", tc.in, err, tc.ok)
			continue
		}
		if tc.ok && loc.String() != tc.want {
			t.Errorf("parseTZ(%q) = %s, want %s", tc.in, loc, tc.want)
		}
	}
}
