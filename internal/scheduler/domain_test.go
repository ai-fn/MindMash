package scheduler

import (
	"testing"
	"time"
)

func mustLoc(t *testing.T, tz string) *time.Location {
	loc, err := time.LoadLocation(tz)
	if err != nil {
		t.Fatalf("load location %q: %v", tz, err)
	}
	return loc
}

func TestDayStart(t *testing.T) {
	utc := time.UTC
	tokyo := mustLoc(t, "Asia/Tokyo")

	tests := []struct {
		name         string
		now          time.Time
		tz           string
		rolloverHour int32
		want         time.Time
	}{
		{
			name:         "after rollover same day UTC",
			now:          time.Date(2026, 6, 19, 10, 0, 0, 0, utc),
			tz:           "UTC",
			rolloverHour: 4,
			want:         time.Date(2026, 6, 19, 4, 0, 0, 0, utc),
		},
		{
			name:         "before rollover counts as previous day UTC",
			now:          time.Date(2026, 6, 19, 2, 0, 0, 0, utc),
			tz:           "UTC",
			rolloverHour: 4,
			want:         time.Date(2026, 6, 18, 4, 0, 0, 0, utc),
		},
		{
			name:         "exactly at rollover counts as current day",
			now:          time.Date(2026, 6, 19, 4, 0, 0, 0, utc),
			tz:           "UTC",
			rolloverHour: 4,
			want:         time.Date(2026, 6, 19, 4, 0, 0, 0, utc),
		},
		{
			name:         "non-UTC timezone resolved before rollover comparison",
			now:          time.Date(2026, 6, 19, 1, 0, 0, 0, utc), // 10:00 JST
			tz:           "Asia/Tokyo",
			rolloverHour: 4,
			want:         time.Date(2026, 6, 19, 4, 0, 0, 0, tokyo).UTC(),
		},
		{
			name:         "invalid timezone falls back to UTC",
			now:          time.Date(2026, 6, 19, 10, 0, 0, 0, utc),
			tz:           "Not/AZone",
			rolloverHour: 4,
			want:         time.Date(2026, 6, 19, 4, 0, 0, 0, utc),
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := DayStart(tt.now, tt.tz, tt.rolloverHour)
			if !got.Equal(tt.want) {
				t.Errorf("DayStart() = %v, want %v", got, tt.want)
			}
		})
	}
}
