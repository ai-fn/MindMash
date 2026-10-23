package media

import (
	"context"
	"io"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"

	"github.com/mindmesh/mindmesh/internal/platform/blob"
	platformerrors "github.com/mindmesh/mindmesh/internal/platform/errors"
)

// fakeBlob is a no-op blobStore for exercising the pre-persistence validation
// path, where the object store is never touched.
type fakeBlob struct{}

func (fakeBlob) Stat(context.Context, string, string) (blob.ObjectInfo, bool, error) {
	return blob.ObjectInfo{}, false, nil
}
func (fakeBlob) PresignPut(context.Context, string, string, time.Duration, blob.PutConstraints) (blob.PresignedPut, error) {
	return blob.PresignedPut{}, nil
}
func (fakeBlob) PresignGet(context.Context, string, string, time.Duration) (string, error) {
	return "", nil
}
func (fakeBlob) Upload(context.Context, string, string, io.Reader, string) error { return nil }
func (fakeBlob) Download(context.Context, string, string) (io.ReadCloser, error) { return nil, nil }
func (fakeBlob) Delete(context.Context, string, string) error                    { return nil }

// TestPresignValidation confirms a bad request is rejected before any repo or
// object-store access — so repo can be nil here.
func TestPresignValidation(t *testing.T) {
	svc := NewService(nil, fakeBlob{}, "mindmesh", zap.NewNop())
	tn := Tenant{UserID: "u", ProfileID: "p"}

	tests := []struct {
		name  string
		mime  string
		sha   string
		bytes int64
	}{
		{"unsupported mime", "application/x-msdownload", hexSum(0x01), 10},
		{"oversize image", "image/png", hexSum(0x02), 26 * mb},
		{"bad checksum", "image/png", "zzzz", 10},
	}
	for _, tc := range tests {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			_, err := svc.Presign(context.Background(), tn, "f", tc.mime, tc.sha, tc.bytes)
			require.Error(t, err)
			assert.True(t, platformerrors.IsInvalidArgument(err), "want InvalidArgument, got %v", err)
		})
	}
}

func TestMediaIDValidation(t *testing.T) {
	svc := NewService(nil, fakeBlob{}, "mindmesh", zap.NewNop())
	tn := Tenant{UserID: "u", ProfileID: "p"}
	ctx := context.Background()

	tests := []struct {
		name string
		call func() error
	}{
		{"confirm", func() error { _, err := svc.Confirm(ctx, tn, "not-a-uuid"); return err }},
		{"download", func() error { _, err := svc.ResolveDownloadURL(ctx, tn, "not-a-uuid"); return err }},
		{"delete", func() error { return svc.Delete(ctx, tn, "") }},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			err := tc.call()
			assert.True(t, platformerrors.IsInvalidArgument(err), "want InvalidArgument, got %v", err)
		})
	}
}
