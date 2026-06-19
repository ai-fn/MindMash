package scheduler

import (
	"context"

	"google.golang.org/protobuf/types/known/timestamppb"

	schedulerv1 "github.com/mindmesh/mindmesh/api/scheduler/v1"
	"github.com/mindmesh/mindmesh/internal/platform/auth"
	platformerrors "github.com/mindmesh/mindmesh/internal/platform/errors"
	"github.com/mindmesh/mindmesh/pkg/fsrs"
)

// Handler implements the StudyService gRPC interface.
type Handler struct {
	schedulerv1.UnimplementedStudyServiceServer
	svc *Service
}

// NewHandler constructs a Handler.
func NewHandler(svc *Service) *Handler {
	return &Handler{svc: svc}
}

// identity extracts the caller's identity from the context, returning an
// error if the auth interceptor did not inject it.
func identity(ctx context.Context) (auth.IdentityContext, error) {
	id, ok := auth.IdentityFromContext(ctx)
	if !ok {
		return auth.IdentityContext{}, platformerrors.Unauthenticated("missing identity context")
	}
	return id, nil
}

// ─── Proto ↔ domain enum mappers ─────────────────────────────────────────────
//
// Domain CardState/Grade are 0/1-indexed (mirroring the cards.state SMALLINT
// column); the proto enums reserve 0 for *_UNSPECIFIED, so CardState and Grade
// are offset by +1 on the wire. RescheduleAction needs no offset: both domain
// and proto start at 1.

func cardStateToProto(s CardState) schedulerv1.CardState {
	return schedulerv1.CardState(s + 1)
}

func gradeFromProto(g schedulerv1.Grade) Grade {
	return Grade(g)
}

func rescheduleActionFromProto(a schedulerv1.RescheduleAction) RescheduleAction {
	return RescheduleAction(a)
}

// ─── Proto ↔ domain message mappers ──────────────────────────────────────────

func queueCardToProto(qc *QueueCard) *schedulerv1.QueueCard {
	return &schedulerv1.QueueCard{
		CardId:    qc.Card.ID,
		DeckId:    qc.Card.DeckID,
		State:     cardStateToProto(qc.Card.State),
		FrontHtml: qc.FrontHTML,
	}
}

func intervalPreviewToProto(p fsrs.IntervalPreview) *schedulerv1.IntervalPreview {
	return &schedulerv1.IntervalPreview{
		AgainSec: p.Again,
		HardSec:  p.Hard,
		GoodSec:  p.Good,
		EasySec:  p.Easy,
	}
}

func schedulingInfoToProto(c *Card) *schedulerv1.SchedulingInfo {
	info := &schedulerv1.SchedulingInfo{
		State:  cardStateToProto(c.State),
		Reps:   c.Reps,
		Lapses: c.Lapses,
	}
	if c.Due != nil {
		info.Due = timestamppb.New(*c.Due)
	}
	if c.Stability != nil {
		info.Stability = *c.Stability
	}
	if c.Difficulty != nil {
		info.Difficulty = *c.Difficulty
	}
	if c.Due != nil && c.LastReview != nil {
		info.IntervalSec = int64(c.Due.Sub(*c.LastReview).Seconds())
	}
	return info
}

// ─── StudyService ─────────────────────────────────────────────────────────────

func (h *Handler) GetQueue(ctx context.Context, req *schedulerv1.GetQueueRequest) (*schedulerv1.GetQueueResponse, error) {
	id, err := identity(ctx)
	if err != nil {
		return nil, err
	}
	queue, err := h.svc.BuildQueue(ctx, id.UserID, id.ProfileID, req.GetDeckId(), int(req.GetLimit()))
	if err != nil {
		return nil, err
	}
	resp := &schedulerv1.GetQueueResponse{}
	for i := range queue {
		resp.Cards = append(resp.Cards, queueCardToProto(&queue[i]))
	}
	return resp, nil
}

func (h *Handler) GetPreview(ctx context.Context, req *schedulerv1.GetPreviewRequest) (*schedulerv1.GetPreviewResponse, error) {
	id, err := identity(ctx)
	if err != nil {
		return nil, err
	}
	preview, err := h.svc.Preview(ctx, id.UserID, req.GetCardId())
	if err != nil {
		return nil, err
	}
	return &schedulerv1.GetPreviewResponse{Preview: intervalPreviewToProto(preview)}, nil
}

func (h *Handler) Answer(ctx context.Context, req *schedulerv1.AnswerRequest) (*schedulerv1.AnswerResponse, error) {
	id, err := identity(ctx)
	if err != nil {
		return nil, err
	}
	c, preview, err := h.svc.Answer(ctx, id.UserID, id.ProfileID, req.GetCardId(), gradeFromProto(req.GetGrade()), req.GetTimeMs())
	if err != nil {
		return nil, err
	}
	return &schedulerv1.AnswerResponse{
		Scheduling: schedulingInfoToProto(c),
		Preview:    intervalPreviewToProto(preview),
	}, nil
}

func (h *Handler) Reschedule(ctx context.Context, req *schedulerv1.RescheduleRequest) (*schedulerv1.RescheduleResponse, error) {
	id, err := identity(ctx)
	if err != nil {
		return nil, err
	}
	if _, err := h.svc.Reschedule(ctx, id.UserID, req.GetCardId(), rescheduleActionFromProto(req.GetAction())); err != nil {
		return nil, err
	}
	return &schedulerv1.RescheduleResponse{}, nil
}

func (h *Handler) GetCounts(ctx context.Context, req *schedulerv1.GetCountsRequest) (*schedulerv1.GetCountsResponse, error) {
	id, err := identity(ctx)
	if err != nil {
		return nil, err
	}
	counts, err := h.svc.GetCounts(ctx, id.UserID, req.GetDeckId())
	if err != nil {
		return nil, err
	}
	return &schedulerv1.GetCountsResponse{
		NewCount:      counts.New,
		LearningCount: counts.Learning,
		ReviewCount:   counts.Review,
	}, nil
}
