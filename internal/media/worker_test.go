package media

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestScaledSize(t *testing.T) {
	tests := []struct {
		name         string
		w, h, edge   int
		wantW, wantH int
	}{
		{"no upscale when small", 100, 80, 256, 100, 80},
		{"landscape clamps width", 512, 256, 256, 256, 128},
		{"portrait clamps height", 256, 512, 256, 128, 256},
		{"square", 1000, 1000, 256, 256, 256},
		{"degenerate zero", 0, 0, 256, 1, 1},
		{"exactly at edge", 256, 256, 256, 256, 256},
	}
	for _, tc := range tests {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			gw, gh := scaledSize(tc.w, tc.h, tc.edge)
			assert.Equal(t, tc.wantW, gw)
			assert.Equal(t, tc.wantH, gh)
			assert.LessOrEqual(t, gw, tc.edge)
			assert.LessOrEqual(t, gh, tc.edge)
		})
	}
}

func TestHasAlpha(t *testing.T) {
	assert.True(t, hasAlpha("png"))
	assert.True(t, hasAlpha("gif"))
	assert.True(t, hasAlpha("webp"))
	assert.False(t, hasAlpha("jpeg"))
}
