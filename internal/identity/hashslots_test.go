package identity

import (
	"math"
	"testing"
)

func TestDefaultHashSlots(t *testing.T) {
	const mib = 1 << 20
	for _, tc := range []struct {
		name     string
		procs    int
		memLimit int64
		want     int
	}{
		{"no memory limit: one per P", 8, math.MaxInt64, 8},
		{"memory limit caps slots at half the budget", 32, 512 * mib, 4},
		{"procs below the memory cap", 2, 512 * mib, 2},
		{"tiny limit still allows one hash", 4, 64 * mib, 1},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := defaultHashSlots(tc.procs, tc.memLimit); got != tc.want {
				t.Errorf("defaultHashSlots(%d, %d MiB) = %d, want %d", tc.procs, tc.memLimit/mib, got, tc.want)
			}
		})
	}
}
