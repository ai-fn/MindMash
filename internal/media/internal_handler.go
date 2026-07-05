package media

import (
	"context"
	"encoding/hex"

	mediav1 "github.com/mindmesh/mindmesh/api/media/v1"
)

// InternalHandler implements MediaInternalService — the gRPC-only surface trusted
// peer services (e.g. Sync) call to reconcile a client's held media.
type InternalHandler struct {
	mediav1.UnimplementedMediaInternalServiceServer
	svc *Service
}

// NewInternalHandler wires the internal handler.
func NewInternalHandler(svc *Service) *InternalHandler { return &InternalHandler{svc: svc} }

// ListChecksums returns the caller's ready media checksums at or above since_usn.
func (h *InternalHandler) ListChecksums(ctx context.Context, req *mediav1.ListChecksumsRequest) (*mediav1.ListChecksumsResponse, error) {
	t, err := tenant(ctx)
	if err != nil {
		return nil, err
	}
	items, err := h.svc.ListChecksums(ctx, t, req.GetSinceUsn())
	if err != nil {
		return nil, err
	}
	resp := &mediav1.ListChecksumsResponse{Items: make([]*mediav1.MediaChecksum, 0, len(items))}
	for _, c := range items {
		resp.Items = append(resp.Items, &mediav1.MediaChecksum{
			MediaId:  c.MediaID,
			Checksum: hex.EncodeToString(c.Checksum),
			Usn:      c.USN,
			Status:   c.Status,
		})
	}
	return resp, nil
}
