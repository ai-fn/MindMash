package media

import (
	"bytes"
	"context"
	"encoding/binary"
	"errors"
	"hash/crc32"
	"image"
	"image/png"
	"io"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"
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

// blobWith serves one object's bytes to the worker.
type blobWith struct {
	fakeBlob
	data []byte
}

func (b blobWith) Download(context.Context, string, string) (io.ReadCloser, error) {
	return io.NopCloser(bytes.NewReader(b.data)), nil
}

// pngHeader is a PNG that only declares w×h: enough for DecodeConfig, nothing to decode.
func pngHeader(w, h uint32) []byte {
	ihdr := []byte("IHDR")
	ihdr = binary.BigEndian.AppendUint32(ihdr, w)
	ihdr = binary.BigEndian.AppendUint32(ihdr, h)
	ihdr = append(ihdr, 8, 6, 0, 0, 0) // 8-bit RGBA, default compression/filter, no interlace
	var out bytes.Buffer
	out.WriteString("\x89PNG\r\n\x1a\n")
	out.Write(binary.BigEndian.AppendUint32(nil, uint32(len(ihdr)-4)))
	out.Write(ihdr)
	out.Write(binary.BigEndian.AppendUint32(nil, crc32.ChecksumIEEE(ihdr)))
	return out.Bytes()
}

func TestRenderThumbRejectsHostileImages(t *testing.T) {
	var small bytes.Buffer
	require.NoError(t, png.Encode(&small, image.NewRGBA(image.Rect(0, 0, 4, 4))))
	bomb := pngHeader(100_000, 100_000)

	tests := []struct {
		name    string
		data    []byte
		bytes   int64
		wantErr string // "" = decodes
	}{
		{"decodes a normal image", small.Bytes(), int64(small.Len()), ""},
		{"refuses a decompression bomb before decoding", bomb, int64(len(bomb)), "exceeds"},
		{"refuses an object larger than confirmed", small.Bytes(), int64(small.Len() - 1), "larger than"},
		{"refuses non-image bytes", []byte("not an image"), 12, "undecodable"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			w := NewWorker(nil, blobWith{data: tc.data}, "b", zap.NewNop())
			thumb, err := w.renderThumb(context.Background(), &Media{S3Key: "k", Bytes: tc.bytes})
			if tc.wantErr != "" {
				assert.True(t, errors.Is(err, errBadImage), "want errBadImage, got %v", err)
				assert.ErrorContains(t, err, tc.wantErr)
				return
			}
			require.NoError(t, err)
			assert.Equal(t, [2]int{4, 4}, [2]int{thumb.width, thumb.height})
		})
	}
}
