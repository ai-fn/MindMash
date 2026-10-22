// Package media implements the MindMesh Media service: presigned uploads,
// content-addressed dedupe, delivery via redirect, and async image processing.
package media

import (
	"encoding/hex"
	"fmt"
	"strings"
	"time"

	platformerrors "github.com/mindmesh/mindmesh/internal/platform/errors"
)

// Media lifecycle states.
const (
	StatusPending = "pending" // presigned, awaiting client PUT + confirm
	StatusReady   = "ready"   // confirmed; blob verified present in S3
)

// Media kinds, derived from the mime type prefix.
const (
	KindImage = "image"
	KindAudio = "audio"
	KindVideo = "video"
	KindLatex = "latex"
	KindOther = "other"
)

const (
	presignTTL  = 5 * time.Minute
	downloadTTL = 15 * time.Minute
)

// thumbVariant is the single derived rendition produced this slice.
const (
	thumbVariant = "thumb"
	thumbMaxEdge = 256 // longest-edge px for the thumbnail
)

// Media is the domain entity mirroring a row in the media table.
type Media struct {
	ID        string
	UserID    string
	ProfileID string
	Checksum  []byte // raw SHA-256
	Kind      string
	Mime      string
	S3Key     string
	Bytes     int64
	Status    string
	USN       int64
	Mtime     time.Time
	CreatedAt time.Time
}

// ChecksumHex returns the lowercase hex encoding of the checksum for the API.
func (m *Media) ChecksumHex() string { return hex.EncodeToString(m.Checksum) }

// allowedMime maps each accepted content type to its kind and max size in bytes.
// Audio/video are served but not transcoded (only images are processed).
type mimeRule struct {
	kind    string
	maxSize int64
}

const (
	mb = 1 << 20
)

var allowedMime = map[string]mimeRule{
	"image/jpeg": {KindImage, 25 * mb},
	"image/png":  {KindImage, 25 * mb},
	"image/gif":  {KindImage, 25 * mb},
	"image/webp": {KindImage, 25 * mb},
	"audio/mpeg": {KindAudio, 50 * mb},
	"audio/ogg":  {KindAudio, 50 * mb},
	"audio/mp4":  {KindAudio, 50 * mb},
	"audio/webm": {KindAudio, 50 * mb},
	"video/mp4":  {KindVideo, 200 * mb},
	"video/webm": {KindVideo, 200 * mb},
}

// UploadRequest is the validated form of a presign request.
type UploadRequest struct {
	Mime     string
	Checksum []byte // raw SHA-256 (32 bytes)
	Bytes    int64
	Kind     string
}

// ValidateUpload checks the declared mime against the allowlist and the declared
// size against the per-kind cap, and decodes the hex checksum. It returns a typed
// InvalidArgument error on any violation.
func ValidateUpload(mime, sha256Hex string, bytes int64) (UploadRequest, error) {
	mime = strings.ToLower(strings.TrimSpace(mime))
	rule, ok := allowedMime[mime]
	if !ok {
		return UploadRequest{}, platformerrors.InvalidArgument(fmt.Sprintf("unsupported mime type %q", mime))
	}
	if bytes <= 0 {
		return UploadRequest{}, platformerrors.InvalidArgument("bytes must be positive")
	}
	if bytes > rule.maxSize {
		return UploadRequest{}, platformerrors.InvalidArgument(
			fmt.Sprintf("%s exceeds the %d-byte limit for %s", mime, rule.maxSize, rule.kind))
	}
	sum, err := hex.DecodeString(strings.ToLower(strings.TrimSpace(sha256Hex)))
	if err != nil || len(sum) != 32 {
		return UploadRequest{}, platformerrors.InvalidArgument("sha256 must be a 64-character hex SHA-256")
	}
	return UploadRequest{
		Mime:     mime,
		Checksum: sum,
		Bytes:    bytes,
		Kind:     rule.kind,
	}, nil
}

// Keys are per user (a shared key would let anyone who knows a hash claim the bytes) and
// extension-free, so re-presigning with another mime never strands an upload under an old key.
func blobPrefix(userID string, checksum []byte) string {
	return "u/" + userID + "/" + hex.EncodeToString(checksum) + "/"
}

// s3KeyFor returns the original-blob key, e.g. "u/<user_id>/<hex>/original".
func s3KeyFor(userID string, checksum []byte) string {
	return blobPrefix(userID, checksum) + "original"
}

// variantKeyFor returns a derived rendition's key, e.g. "u/<user_id>/<hex>/thumb.jpg".
func variantKeyFor(userID string, checksum []byte, variant, ext string) string {
	return blobPrefix(userID, checksum) + variant + "." + ext
}
