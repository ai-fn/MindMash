package search

import (
	"context"
	"strings"

	"google.golang.org/protobuf/types/known/timestamppb"

	searchv1 "github.com/mindmesh/mindmesh/api/search/v1"
	"github.com/mindmesh/mindmesh/internal/platform/auth"
	platformerrors "github.com/mindmesh/mindmesh/internal/platform/errors"
	"github.com/mindmesh/mindmesh/internal/platform/validate"
	"github.com/mindmesh/mindmesh/pkg/searchdsl"
)

// pagination bounds for the Search endpoint.
const (
	defaultLimit = 50
	maxLimit     = 200
)

// Handler implements the SearchService gRPC server. It parses the DSL, compiles
// it to an OpenSearch query hard-filtered to the caller's tenant, and returns a
// keyset page of card summaries.
type Handler struct {
	searchv1.UnimplementedSearchServiceServer
	repo *Repo
}

// NewHandler wires the Search handler.
func NewHandler(repo *Repo) *Handler {
	return &Handler{repo: repo}
}

// Search handles GET /v1/search. Tenancy comes from the gateway-injected JWT
// context, never from the request, so a user can only ever see their own cards.
func (h *Handler) Search(ctx context.Context, req *searchv1.SearchRequest) (*searchv1.SearchResponse, error) {
	id, ok := auth.IdentityFromContext(ctx)
	if !ok {
		return nil, platformerrors.Unauthenticated("missing identity context")
	}

	if err := validate.Text("q", req.GetQ(), validate.MaxQueryBytes); err != nil {
		return nil, err
	}
	// An empty query lists all of the tenant's cards (nil node = unconstrained).
	var node searchdsl.Node
	if q := strings.TrimSpace(req.GetQ()); q != "" {
		parsed, err := searchdsl.Parse(q)
		if err != nil {
			return nil, platformerrors.InvalidArgument("invalid query: " + err.Error())
		}
		node = parsed
	}

	searchAfter, err := decodeCursor(req.GetCursor())
	if err != nil {
		return nil, platformerrors.InvalidArgument("invalid cursor")
	}

	tenant := Tenant{UserID: id.UserID}
	query := BuildQuery(node, tenant, Opts{})

	result, err := h.repo.Search(ctx, id.UserID, query, searchAfter, clampLimit(req.GetLimit()))
	if err != nil {
		// The index being down is a degraded-but-recoverable condition; surface it
		// as Unavailable (503) rather than a generic Internal error.
		return nil, platformerrors.Unavailable("search backend unavailable", err)
	}

	resp := &searchv1.SearchResponse{
		NextCursor: result.NextCursor,
		Total:      int64(result.Total),
	}
	for i := range result.Docs {
		resp.Results = append(resp.Results, documentToSummary(&result.Docs[i]))
	}
	return resp, nil
}

// clampLimit applies the page-size bounds: non-positive falls back to the
// default, oversized requests are capped.
func clampLimit(limit int32) int {
	switch {
	case limit <= 0:
		return defaultLimit
	case limit > maxLimit:
		return maxLimit
	default:
		return int(limit)
	}
}

// documentToSummary projects an index document onto the public card summary,
// dropping the internal tenant and versioning fields.
func documentToSummary(d *Document) *searchv1.CardSummary {
	s := &searchv1.CardSummary{
		CardId:      d.CardID,
		NoteId:      d.NoteID,
		DeckId:      d.DeckID,
		DeckPath:    d.DeckPath,
		TemplateOrd: d.TemplateOrd,
		State:       d.State,
		Suspended:   d.Suspended,
		Ivl:         d.Ivl,
		Reps:        d.Reps,
		Lapses:      d.Lapses,
		Flags:       d.Flags,
		Tags:        d.Tags,
		FieldsText:  d.FieldsText,
	}
	if d.Due != nil {
		s.Due = timestamppb.New(*d.Due)
	}
	if !d.AddedAt.IsZero() {
		s.AddedAt = timestamppb.New(d.AddedAt)
	}
	return s
}
