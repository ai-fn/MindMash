package media

import (
	"encoding/hex"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	platformerrors "github.com/mindmesh/mindmesh/internal/platform/errors"
)

func hexSum(b byte) string {
	sum := make([]byte, 32)
	for i := range sum {
		sum[i] = b
	}
	return hex.EncodeToString(sum)
}

func TestValidateUpload(t *testing.T) {
	tests := []struct {
		name     string
		mime     string
		sha      string
		bytes    int64
		wantKind string
		wantExt  string
		wantErr  bool
	}{
		{"jpeg ok", "image/jpeg", hexSum(0xab), 1024, KindImage, "jpg", false},
		{"png ok", "image/png", hexSum(0x01), 500, KindImage, "png", false},
		{"mp3 audio", "audio/mpeg", hexSum(0x02), 1024, KindAudio, "mp3", false},
		{"mp4 video", "video/mp4", hexSum(0x03), 1024, KindVideo, "mp4", false},
		{"mime case-insensitive", "IMAGE/JPEG", hexSum(0x04), 10, KindImage, "jpg", false},
		{"unsupported mime", "application/pdf", hexSum(0x05), 10, "", "", true},
		{"zero bytes", "image/png", hexSum(0x06), 0, "", "", true},
		{"negative bytes", "image/png", hexSum(0x06), -1, "", "", true},
		{"over image cap", "image/png", hexSum(0x07), 26 * mb, "", "", true},
		{"image under-cap where video would fit", "image/png", hexSum(0x08), 30 * mb, "", "", true},
		{"bad hex checksum", "image/png", "nothex", 10, "", "", true},
		{"short checksum", "image/png", "abcd", 10, "", "", true},
	}
	for _, tc := range tests {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			got, err := ValidateUpload(tc.mime, tc.sha, tc.bytes)
			if tc.wantErr {
				require.Error(t, err)
				assert.True(t, platformerrors.IsInvalidArgument(err), "want InvalidArgument, got %v", err)
				return
			}
			require.NoError(t, err)
			assert.Equal(t, tc.wantKind, got.Kind)
			assert.Equal(t, tc.wantExt, got.Ext)
			assert.Len(t, got.Checksum, 32)
		})
	}
}

func TestKeyDerivation(t *testing.T) {
	sum, err := hex.DecodeString(hexSum(0xcd))
	require.NoError(t, err)

	orig := s3KeyFor(sum, "jpg")
	assert.True(t, strings.HasSuffix(orig, ".jpg"))
	assert.Equal(t, hex.EncodeToString(sum)+".jpg", orig)

	variant := variantKeyFor(sum, thumbVariant, "jpg")
	assert.Equal(t, hex.EncodeToString(sum)+"/thumb.jpg", variant)
	// Original and variant share the checksum prefix but differ by separator.
	assert.True(t, strings.HasPrefix(variant, hex.EncodeToString(sum)+"/"))
}

func TestChecksumHex(t *testing.T) {
	m := &Media{Checksum: []byte{0xde, 0xad, 0xbe, 0xef}}
	assert.Equal(t, "deadbeef", m.ChecksumHex())
	assert.Equal(t, "deadbeef", checksumHex(m.Checksum))
}
