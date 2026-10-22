package media

import (
	"encoding/hex"
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
		wantMime string
		wantErr  bool
	}{
		{"jpeg ok", "image/jpeg", hexSum(0xab), 1024, KindImage, "image/jpeg", false},
		{"png ok", "image/png", hexSum(0x01), 500, KindImage, "image/png", false},
		{"mp3 audio", "audio/mpeg", hexSum(0x02), 1024, KindAudio, "audio/mpeg", false},
		{"mp4 video", "video/mp4", hexSum(0x03), 1024, KindVideo, "video/mp4", false},
		{"mime case-insensitive", "IMAGE/JPEG", hexSum(0x04), 10, KindImage, "image/jpeg", false},
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
			assert.Equal(t, tc.wantMime, got.Mime)
			assert.Len(t, got.Checksum, 32)
		})
	}
}

func TestKeyDerivation(t *testing.T) {
	sum, err := hex.DecodeString(hexSum(0xcd))
	require.NoError(t, err)
	h := hex.EncodeToString(sum)

	orig := s3KeyFor("user-1", sum)
	assert.Equal(t, "u/user-1/"+h+"/original", orig)
	assert.Equal(t, "u/user-1/"+h+"/thumb.jpg", variantKeyFor("user-1", sum, thumbVariant, "jpg"))
	assert.NotEqual(t, orig, s3KeyFor("user-2", sum), "keys must not be shared across users")
}

func TestChecksumHex(t *testing.T) {
	m := &Media{Checksum: []byte{0xde, 0xad, 0xbe, 0xef}}
	assert.Equal(t, "deadbeef", m.ChecksumHex())
	assert.Equal(t, "deadbeef", checksumHex(m.Checksum))
}
