package main

import "testing"

func TestParseMode(t *testing.T) {
	for _, tc := range []struct {
		in   string
		want mode
		ok   bool
	}{
		{"", mode{api: true, worker: true}, true},
		{"all", mode{api: true, worker: true}, true},
		{"api", mode{api: true}, true},
		{"worker", mode{worker: true}, true},
		{"workers", mode{}, false},
	} {
		got, err := parseMode(tc.in)
		if (err == nil) != tc.ok || got != tc.want {
			t.Errorf("parseMode(%q) = %+v, %v; want %+v, ok=%v", tc.in, got, err, tc.want, tc.ok)
		}
	}
}
