package media

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"image"
	_ "image/gif" // register GIF decoder
	"image/jpeg"
	"image/png"
	"io"

	"go.uber.org/zap"
	xdraw "golang.org/x/image/draw"
	_ "golang.org/x/image/webp" // register WebP decoder

	platformevents "github.com/mindmesh/mindmesh/internal/platform/events"
)

// errBadImage is acked, not retried: it can never decode, and the original is still served.
var errBadImage = errors.New("media: undecodable image")

// jpegQuality is the encode quality for thumbnails without alpha.
const jpegQuality = 85

// maxDecodePixels bounds a decode to ~800 MB worst case (progressive JPEG): a tiny file can declare huge dimensions.
const maxDecodePixels = 50_000_000

// Worker thumbnails images and acks other kinds; the original is always served, so a failure
// never blocks uploads.
type Worker struct {
	repo   *Repo
	blob   blobStore
	bucket string
	log    *zap.Logger
}

// NewWorker wires the processing worker.
func NewWorker(repo *Repo, b blobStore, bucket string, log *zap.Logger) *Worker {
	return &Worker{repo: repo, blob: b, bucket: bucket, log: log}
}

// Handle processes one media.uploaded message. Returning an error leaves the
// offset uncommitted so the message is redelivered.
func (w *Worker) Handle(ctx context.Context, msg platformevents.Message) error {
	var p uploadedPayload
	if err := json.Unmarshal(msg.Value, &p); err != nil {
		// A malformed payload will never succeed; log and ack to avoid a poison loop.
		w.log.Error("media worker: bad payload", zap.Error(err))
		return nil
	}
	if p.Kind != KindImage {
		return nil // audio/video/latex processing is deferred
	}

	m, err := w.repo.GetByID(ctx, p.UserID, p.ID)
	if err != nil {
		return fmt.Errorf("media worker: load %s: %w", p.ID, err)
	}

	thumb, err := w.renderThumb(ctx, m)
	if err != nil {
		// A corrupt/mislabeled image can never be decoded on retry — ack it.
		// Transient errors (download/upload/DB) are returned so Kafka redelivers.
		if errors.Is(err, errBadImage) {
			w.log.Warn("media worker: skipping undecodable image", zap.String("media_id", m.ID), zap.Error(err))
			return nil
		}
		return fmt.Errorf("media worker: render thumb for %s: %w", m.ID, err)
	}

	variantKey := variantKeyFor(m.UserID, m.Checksum, thumbVariant, thumb.ext)
	if err := w.blob.Upload(ctx, w.bucket, variantKey, bytes.NewReader(thumb.data), thumb.mime); err != nil {
		return fmt.Errorf("media worker: upload thumb %s: %w", variantKey, err)
	}
	if err := w.repo.InsertVariant(ctx, Variant{
		UserID:  m.UserID,
		MediaID: m.ID,
		Variant: thumbVariant,
		S3Key:   variantKey,
		Mime:    thumb.mime,
		Width:   thumb.width,
		Height:  thumb.height,
	}); err != nil {
		return fmt.Errorf("media worker: record variant %s: %w", m.ID, err)
	}
	w.log.Debug("media worker: thumbnail produced",
		zap.String("media_id", m.ID), zap.String("key", variantKey))
	return nil
}

type thumbnail struct {
	data          []byte
	mime, ext     string
	width, height int
}

// renderThumb encodes sources with alpha as PNG to keep transparency, the rest as JPEG.
func (w *Worker) renderThumb(ctx context.Context, m *Media) (thumbnail, error) {
	rc, err := w.blob.Download(ctx, w.bucket, m.S3Key)
	if err != nil {
		return thumbnail{}, err
	}
	defer func() { _ = rc.Close() }()

	raw, err := io.ReadAll(io.LimitReader(rc, m.Bytes+1))
	if err != nil {
		return thumbnail{}, err
	}
	if int64(len(raw)) > m.Bytes {
		return thumbnail{}, fmt.Errorf("%w: object larger than its confirmed %d bytes", errBadImage, m.Bytes)
	}
	cfg, _, err := image.DecodeConfig(bytes.NewReader(raw))
	if err != nil {
		return thumbnail{}, fmt.Errorf("%w: %w", errBadImage, err)
	}
	if int64(cfg.Width)*int64(cfg.Height) > maxDecodePixels {
		return thumbnail{}, fmt.Errorf("%w: %dx%d exceeds %d pixels", errBadImage, cfg.Width, cfg.Height, maxDecodePixels)
	}
	src, format, err := image.Decode(bytes.NewReader(raw))
	if err != nil {
		return thumbnail{}, fmt.Errorf("%w: %w", errBadImage, err)
	}

	dw, dh := scaledSize(src.Bounds().Dx(), src.Bounds().Dy(), thumbMaxEdge)
	dst := image.NewRGBA(image.Rect(0, 0, dw, dh))
	xdraw.CatmullRom.Scale(dst, dst.Bounds(), src, src.Bounds(), xdraw.Over, nil)

	var buf bytes.Buffer
	if hasAlpha(format) {
		if err := png.Encode(&buf, dst); err != nil {
			return thumbnail{}, err
		}
		return thumbnail{data: buf.Bytes(), mime: "image/png", ext: "png", width: dw, height: dh}, nil
	}
	if err := jpeg.Encode(&buf, dst, &jpeg.Options{Quality: jpegQuality}); err != nil {
		return thumbnail{}, err
	}
	return thumbnail{data: buf.Bytes(), mime: "image/jpeg", ext: "jpg", width: dw, height: dh}, nil
}

// scaledSize returns the dimensions that fit (w,h) within a maxEdge-length
// longest side, preserving aspect ratio and never upscaling.
func scaledSize(w, h, maxEdge int) (int, int) {
	if w <= 0 || h <= 0 {
		return 1, 1
	}
	if w <= maxEdge && h <= maxEdge {
		return w, h
	}
	if w >= h {
		return maxEdge, max(1, h*maxEdge/w)
	}
	return max(1, w*maxEdge/h), maxEdge
}

// hasAlpha reports whether a decoded image format carries transparency worth
// preserving (PNG, GIF, WebP). JPEG never does.
func hasAlpha(format string) bool {
	switch format {
	case "png", "gif", "webp":
		return true
	default:
		return false
	}
}
