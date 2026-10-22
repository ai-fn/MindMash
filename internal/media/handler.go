package media

import (
	"context"

	mediav1 "github.com/mindmesh/mindmesh/api/media/v1"
	"github.com/mindmesh/mindmesh/internal/platform/auth"
)

// Handler implements the external MediaService gRPC server. Tenancy is always
// taken from the JWT context, never the request body.
type Handler struct {
	mediav1.UnimplementedMediaServiceServer
	svc *Service
}

// NewHandler wires the media gRPC handler.
func NewHandler(svc *Service) *Handler { return &Handler{svc: svc} }

func tenant(ctx context.Context) (Tenant, error) {
	id, err := auth.RequireIdentity(ctx)
	if err != nil {
		return Tenant{}, err
	}
	return Tenant{UserID: id.UserID, ProfileID: id.ProfileID}, nil
}

// Presign handles POST /v1/media:presign.
func (h *Handler) Presign(ctx context.Context, req *mediav1.PresignRequest) (*mediav1.PresignResponse, error) {
	t, err := tenant(ctx)
	if err != nil {
		return nil, err
	}
	res, err := h.svc.Presign(ctx, t, req.GetFilename(), req.GetMime(), req.GetSha256(), req.GetBytes())
	if err != nil {
		return nil, err
	}
	return &mediav1.PresignResponse{
		MediaId:       res.MediaID,
		UploadUrl:     res.UploadURL,
		UploadHeaders: res.UploadHeaders,
		AlreadyExists: res.AlreadyExists,
	}, nil
}

// Confirm handles POST /v1/media/{media_id}:confirm.
func (h *Handler) Confirm(ctx context.Context, req *mediav1.ConfirmRequest) (*mediav1.ConfirmResponse, error) {
	t, err := tenant(ctx)
	if err != nil {
		return nil, err
	}
	m, err := h.svc.Confirm(ctx, t, req.GetMediaId())
	if err != nil {
		return nil, err
	}
	return &mediav1.ConfirmResponse{Media: toProto(m)}, nil
}

// Delete handles DELETE /v1/media/{media_id}.
func (h *Handler) Delete(ctx context.Context, req *mediav1.DeleteRequest) (*mediav1.DeleteResponse, error) {
	t, err := tenant(ctx)
	if err != nil {
		return nil, err
	}
	if err := h.svc.Delete(ctx, t, req.GetMediaId()); err != nil {
		return nil, err
	}
	return &mediav1.DeleteResponse{}, nil
}

// toProto maps a domain Media to its wire form (checksum as lowercase hex).
func toProto(m *Media) *mediav1.Media {
	return &mediav1.Media{
		Id:       m.ID,
		Checksum: m.ChecksumHex(),
		Kind:     m.Kind,
		Mime:     m.Mime,
		Bytes:    m.Bytes,
		Status:   m.Status,
		Usn:      m.USN,
	}
}
