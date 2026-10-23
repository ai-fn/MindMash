package collection

import (
	"context"

	"google.golang.org/protobuf/types/known/timestamppb"

	collectionv1 "github.com/mindmesh/mindmesh/api/collection/v1"
	"github.com/mindmesh/mindmesh/internal/platform/auth"
	platformerrors "github.com/mindmesh/mindmesh/internal/platform/errors"
)

// Handler implements the five public Collection gRPC service interfaces.
type Handler struct {
	collectionv1.UnimplementedNoteTypeServiceServer
	collectionv1.UnimplementedNoteServiceServer
	collectionv1.UnimplementedCardServiceServer
	collectionv1.UnimplementedDeckServiceServer
	collectionv1.UnimplementedFilteredDeckServiceServer

	svc *Service
}

// NewHandler constructs a Handler.
func NewHandler(svc *Service) *Handler {
	return &Handler{svc: svc}
}

// InternalHandler implements the CollectionInternalService gRPC interface.
// It is kept separate from Handler because both NoteService and
// CollectionInternalService define a GetNote method with different signatures;
// Go does not allow two methods with the same name on one struct.
type InternalHandler struct {
	collectionv1.UnimplementedCollectionInternalServiceServer
	svc *Service
}

// NewInternalHandler constructs an InternalHandler.
func NewInternalHandler(svc *Service) *InternalHandler {
	return &InternalHandler{svc: svc}
}

// identity extracts the caller's identity from the context, returning an error
// if the auth interceptor did not inject it.
func identity(ctx context.Context) (auth.IdentityContext, error) {
	id, ok := auth.IdentityFromContext(ctx)
	if !ok {
		return auth.IdentityContext{}, platformerrors.Unauthenticated("missing identity context")
	}
	return id, nil
}

// ─── Proto ↔ domain mappers ──────────────────────────────────────────────────

func noteTypeToProto(nt *NoteType) *collectionv1.NoteType {
	p := &collectionv1.NoteType{
		Id:    nt.ID,
		Name:  nt.Name,
		Css:   nt.CSS,
		Usn:   nt.USN,
		Mtime: timestamppb.New(nt.Mtime),
	}
	for _, f := range nt.Fields {
		p.Fields = append(p.Fields, &collectionv1.Field{Name: f.Name, Sticky: f.Sticky, Rtl: f.RTL, Language: f.Language})
	}
	for _, t := range nt.CardTemplates {
		p.CardTemplates = append(p.CardTemplates, &collectionv1.CardTemplate{Name: t.Name, Front: t.Front, Back: t.Back})
	}
	return p
}

func fieldsFromProto(pf []*collectionv1.Field) []Field {
	out := make([]Field, 0, len(pf))
	for _, f := range pf {
		out = append(out, Field{Name: f.GetName(), Sticky: f.GetSticky(), RTL: f.GetRtl(), Language: f.GetLanguage()})
	}
	return out
}

func templatesFromProto(pt []*collectionv1.CardTemplate) []CardTemplate {
	out := make([]CardTemplate, 0, len(pt))
	for _, t := range pt {
		out = append(out, CardTemplate{Name: t.GetName(), Front: t.GetFront(), Back: t.GetBack()})
	}
	return out
}

func noteToProto(n *Note) *collectionv1.Note {
	return &collectionv1.Note{
		Id:         n.ID,
		NoteTypeId: n.NoteTypeID,
		Fields:     n.Fields,
		Tags:       n.Tags,
		Flags:      n.Flags,
		Usn:        n.USN,
		Mtime:      timestamppb.New(n.Mtime),
	}
}

// cardStateToProto converts the DB state (0-based) to the proto enum (+1 offset).
func cardStateToProto(s CardState) collectionv1.CardState {
	return collectionv1.CardState(int32(s) + 1)
}

func cardToProto(c *Card) *collectionv1.Card {
	p := &collectionv1.Card{
		Id:          c.ID,
		NoteId:      c.NoteID,
		DeckId:      c.DeckID,
		TemplateOrd: int32(c.TemplateOrd),
		State:       cardStateToProto(c.State),
		Queue:       int32(c.Queue),
		Reps:        int32(c.Reps),
		Lapses:      int32(c.Lapses),
		Usn:         c.USN,
		Mtime:       timestamppb.New(c.Mtime),
	}
	if c.Due != nil {
		p.Due = timestamppb.New(*c.Due)
	}
	if c.Stability != nil {
		p.Stability = *c.Stability
	}
	if c.Difficulty != nil {
		p.Difficulty = *c.Difficulty
	}
	if c.LastReview != nil {
		p.LastReview = timestamppb.New(*c.LastReview)
	}
	return p
}

func deckToProto(d *Deck) *collectionv1.Deck {
	p := &collectionv1.Deck{
		Id:    d.ID,
		Name:  d.Name,
		Path:  d.Path,
		Usn:   d.USN,
		Mtime: timestamppb.New(d.Mtime),
	}
	if d.ParentID != nil {
		p.ParentId = *d.ParentID
	}
	if d.ConfigID != nil {
		p.ConfigId = *d.ConfigID
	}
	return p
}

func deckConfigToProto(dc *DeckConfig) *collectionv1.DeckConfig {
	return &collectionv1.DeckConfig{
		Id:               dc.ID,
		Name:             dc.Name,
		NewPerDay:        dc.NewPerDay,
		RevPerDay:        dc.RevPerDay,
		DesiredRetention: dc.DesiredRetention,
		FsrsWeights:      dc.FSRSWeights,
		LearningSteps:    dc.LearningSteps,
		RolloverHour:     dc.RolloverHour,
		Timezone:         dc.Timezone,
		Usn:              dc.USN,
		Mtime:            timestamppb.New(dc.Mtime),
	}
}

func filteredDeckToProto(fd *FilteredDeck) *collectionv1.FilteredDeck {
	return &collectionv1.FilteredDeck{
		Id:    fd.ID,
		Name:  fd.Name,
		Query: fd.Query,
		Usn:   fd.USN,
		Mtime: timestamppb.New(fd.Mtime),
	}
}

func pageFromProto(token string, size int32) Page {
	return Page{Token: token, Size: int(size)}
}

// ─── NoteTypeService ─────────────────────────────────────────────────────────

func (h *Handler) ListNoteTypes(ctx context.Context, req *collectionv1.ListNoteTypesRequest) (*collectionv1.ListNoteTypesResponse, error) {
	id, err := identity(ctx)
	if err != nil {
		return nil, err
	}
	items, next, err := h.svc.ListNoteTypes(ctx, id.UserID, pageFromProto(req.GetPageToken(), req.GetPageSize()))
	if err != nil {
		return nil, err
	}
	resp := &collectionv1.ListNoteTypesResponse{NextPageToken: next}
	for i := range items {
		resp.Items = append(resp.Items, noteTypeToProto(&items[i]))
	}
	return resp, nil
}

func (h *Handler) GetNoteType(ctx context.Context, req *collectionv1.GetNoteTypeRequest) (*collectionv1.GetNoteTypeResponse, error) {
	id, err := identity(ctx)
	if err != nil {
		return nil, err
	}
	nt, err := h.svc.GetNoteType(ctx, id.UserID, req.GetId())
	if err != nil {
		return nil, err
	}
	return &collectionv1.GetNoteTypeResponse{NoteType: noteTypeToProto(nt)}, nil
}

func (h *Handler) CreateNoteType(ctx context.Context, req *collectionv1.CreateNoteTypeRequest) (*collectionv1.CreateNoteTypeResponse, error) {
	id, err := identity(ctx)
	if err != nil {
		return nil, err
	}
	nt, err := h.svc.CreateNoteType(ctx, id.UserID, id.ProfileID,
		req.GetName(), req.GetCss(),
		fieldsFromProto(req.GetFields()),
		templatesFromProto(req.GetCardTemplates()))
	if err != nil {
		return nil, err
	}
	return &collectionv1.CreateNoteTypeResponse{NoteType: noteTypeToProto(nt)}, nil
}

func (h *Handler) UpdateNoteType(ctx context.Context, req *collectionv1.UpdateNoteTypeRequest) (*collectionv1.UpdateNoteTypeResponse, error) {
	id, err := identity(ctx)
	if err != nil {
		return nil, err
	}
	nt, err := h.svc.UpdateNoteType(ctx, id.UserID, req.GetId(), req.GetExpectedUsn(),
		req.GetName(), req.GetCss(),
		fieldsFromProto(req.GetFields()),
		templatesFromProto(req.GetCardTemplates()))
	if err != nil {
		return nil, err
	}
	return &collectionv1.UpdateNoteTypeResponse{NoteType: noteTypeToProto(nt)}, nil
}

func (h *Handler) DeleteNoteType(ctx context.Context, req *collectionv1.DeleteNoteTypeRequest) (*collectionv1.DeleteNoteTypeResponse, error) {
	id, err := identity(ctx)
	if err != nil {
		return nil, err
	}
	if err := h.svc.DeleteNoteType(ctx, id.UserID, req.GetId(), req.GetExpectedUsn()); err != nil {
		return nil, err
	}
	return &collectionv1.DeleteNoteTypeResponse{}, nil
}

// ─── NoteService ─────────────────────────────────────────────────────────────

func (h *Handler) GetNote(ctx context.Context, req *collectionv1.GetNoteRequest) (*collectionv1.GetNoteResponse, error) {
	id, err := identity(ctx)
	if err != nil {
		return nil, err
	}
	n, err := h.svc.GetNote(ctx, id.UserID, req.GetId())
	if err != nil {
		return nil, err
	}
	return &collectionv1.GetNoteResponse{Note: noteToProto(n)}, nil
}

func (h *Handler) CreateNote(ctx context.Context, req *collectionv1.CreateNoteRequest) (*collectionv1.CreateNoteResponse, error) {
	id, err := identity(ctx)
	if err != nil {
		return nil, err
	}
	n, err := h.svc.CreateNote(ctx, id.UserID, id.ProfileID,
		req.GetNoteTypeId(), req.GetDeckId(),
		req.GetFields(), req.GetTags(), req.GetFlags())
	if err != nil {
		return nil, err
	}
	return &collectionv1.CreateNoteResponse{Note: noteToProto(n)}, nil
}

func (h *Handler) UpdateNote(ctx context.Context, req *collectionv1.UpdateNoteRequest) (*collectionv1.UpdateNoteResponse, error) {
	id, err := identity(ctx)
	if err != nil {
		return nil, err
	}
	n, err := h.svc.UpdateNote(ctx, id.UserID, req.GetId(), req.GetExpectedUsn(),
		req.GetFields(), req.GetTags(), req.GetFlags())
	if err != nil {
		return nil, err
	}
	return &collectionv1.UpdateNoteResponse{Note: noteToProto(n)}, nil
}

func (h *Handler) DeleteNote(ctx context.Context, req *collectionv1.DeleteNoteRequest) (*collectionv1.DeleteNoteResponse, error) {
	id, err := identity(ctx)
	if err != nil {
		return nil, err
	}
	if err := h.svc.DeleteNote(ctx, id.UserID, req.GetId(), req.GetExpectedUsn()); err != nil {
		return nil, err
	}
	return &collectionv1.DeleteNoteResponse{}, nil
}

// ─── CardService ─────────────────────────────────────────────────────────────

func (h *Handler) GetCard(ctx context.Context, req *collectionv1.GetCardRequest) (*collectionv1.GetCardResponse, error) {
	id, err := identity(ctx)
	if err != nil {
		return nil, err
	}
	card, front, back, err := h.svc.RenderCard(ctx, id.UserID, req.GetId())
	if err != nil {
		return nil, err
	}
	return &collectionv1.GetCardResponse{
		Card:      cardToProto(card),
		FrontHtml: front,
		BackHtml:  back,
	}, nil
}

func (h *Handler) BulkCards(ctx context.Context, req *collectionv1.BulkCardsRequest) (*collectionv1.BulkCardsResponse, error) {
	id, err := identity(ctx)
	if err != nil {
		return nil, err
	}

	var cardIDs []string
	switch s := req.GetSelector().(type) {
	case *collectionv1.BulkCardsRequest_IdsList:
		cardIDs = s.IdsList.GetIds()
	case *collectionv1.BulkCardsRequest_Query:
		return nil, platformerrors.Unimplemented("query-based bulk selection requires the Search service")
	default:
		return nil, platformerrors.InvalidArgument("selector is required")
	}

	args := req.GetArgs()
	var affected int64

	switch req.GetOp() {
	case collectionv1.BulkOp_BULK_OP_MOVE:
		affected, err = h.svc.MoveCards(ctx, id.UserID, cardIDs, args.GetTargetDeckId())
	case collectionv1.BulkOp_BULK_OP_TAG:
		affected, err = h.svc.AddTagToCards(ctx, id.UserID, cardIDs, args.GetTag())
	case collectionv1.BulkOp_BULK_OP_UNTAG:
		affected, err = h.svc.RemoveTagFromCards(ctx, id.UserID, cardIDs, args.GetTag())
	case collectionv1.BulkOp_BULK_OP_FLAG:
		affected, err = h.svc.SetFlagOnCards(ctx, id.UserID, cardIDs, args.GetFlag())
	case collectionv1.BulkOp_BULK_OP_SUSPEND:
		affected, err = h.svc.SuspendCards(ctx, id.UserID, cardIDs)
	case collectionv1.BulkOp_BULK_OP_DELETE:
		affected, err = h.svc.DeleteCards(ctx, id.UserID, cardIDs)
	default:
		return nil, platformerrors.InvalidArgument("unknown bulk op")
	}
	if err != nil {
		return nil, err
	}
	return &collectionv1.BulkCardsResponse{AffectedCount: affected}, nil
}

// ─── DeckService ─────────────────────────────────────────────────────────────

func (h *Handler) ListDecks(ctx context.Context, req *collectionv1.ListDecksRequest) (*collectionv1.ListDecksResponse, error) {
	id, err := identity(ctx)
	if err != nil {
		return nil, err
	}
	items, next, err := h.svc.ListDecks(ctx, id.UserID, pageFromProto(req.GetPageToken(), req.GetPageSize()))
	if err != nil {
		return nil, err
	}
	resp := &collectionv1.ListDecksResponse{NextPageToken: next}
	for i := range items {
		resp.Items = append(resp.Items, deckToProto(&items[i]))
	}
	return resp, nil
}

func (h *Handler) GetDeck(ctx context.Context, req *collectionv1.GetDeckRequest) (*collectionv1.GetDeckResponse, error) {
	id, err := identity(ctx)
	if err != nil {
		return nil, err
	}
	d, err := h.svc.GetDeck(ctx, id.UserID, req.GetId())
	if err != nil {
		return nil, err
	}
	return &collectionv1.GetDeckResponse{Deck: deckToProto(d)}, nil
}

func (h *Handler) CreateDeck(ctx context.Context, req *collectionv1.CreateDeckRequest) (*collectionv1.CreateDeckResponse, error) {
	id, err := identity(ctx)
	if err != nil {
		return nil, err
	}
	d, err := h.svc.CreateDeck(ctx, id.UserID, id.ProfileID,
		req.GetName(), req.GetParentId(), req.GetConfigId())
	if err != nil {
		return nil, err
	}
	return &collectionv1.CreateDeckResponse{Deck: deckToProto(d)}, nil
}

func (h *Handler) UpdateDeck(ctx context.Context, req *collectionv1.UpdateDeckRequest) (*collectionv1.UpdateDeckResponse, error) {
	id, err := identity(ctx)
	if err != nil {
		return nil, err
	}
	d, err := h.svc.UpdateDeck(ctx, id.UserID, req.GetId(), req.GetExpectedUsn(),
		req.GetName(), req.GetParentId(), req.GetConfigId())
	if err != nil {
		return nil, err
	}
	return &collectionv1.UpdateDeckResponse{Deck: deckToProto(d)}, nil
}

func (h *Handler) DeleteDeck(ctx context.Context, req *collectionv1.DeleteDeckRequest) (*collectionv1.DeleteDeckResponse, error) {
	id, err := identity(ctx)
	if err != nil {
		return nil, err
	}
	if err := h.svc.DeleteDeck(ctx, id.UserID, req.GetId(), req.GetExpectedUsn()); err != nil {
		return nil, err
	}
	return &collectionv1.DeleteDeckResponse{}, nil
}

func (h *Handler) ListDeckConfigs(ctx context.Context, req *collectionv1.ListDeckConfigsRequest) (*collectionv1.ListDeckConfigsResponse, error) {
	id, err := identity(ctx)
	if err != nil {
		return nil, err
	}
	items, next, err := h.svc.ListDeckConfigs(ctx, id.UserID, pageFromProto(req.GetPageToken(), req.GetPageSize()))
	if err != nil {
		return nil, err
	}
	resp := &collectionv1.ListDeckConfigsResponse{NextPageToken: next}
	for i := range items {
		resp.Items = append(resp.Items, deckConfigToProto(&items[i]))
	}
	return resp, nil
}

func (h *Handler) GetDeckConfig(ctx context.Context, req *collectionv1.GetDeckConfigRequest) (*collectionv1.GetDeckConfigResponse, error) {
	id, err := identity(ctx)
	if err != nil {
		return nil, err
	}
	dc, err := h.svc.GetDeckConfig(ctx, id.UserID, req.GetId())
	if err != nil {
		return nil, err
	}
	return &collectionv1.GetDeckConfigResponse{DeckConfig: deckConfigToProto(dc)}, nil
}

func (h *Handler) CreateDeckConfig(ctx context.Context, req *collectionv1.CreateDeckConfigRequest) (*collectionv1.CreateDeckConfigResponse, error) {
	id, err := identity(ctx)
	if err != nil {
		return nil, err
	}
	dc, err := h.svc.CreateDeckConfig(ctx, id.UserID, id.ProfileID, DeckConfig{
		Name:             req.GetName(),
		NewPerDay:        req.GetNewPerDay(),
		RevPerDay:        req.GetRevPerDay(),
		DesiredRetention: req.GetDesiredRetention(),
		FSRSWeights:      req.GetFsrsWeights(),
		LearningSteps:    req.GetLearningSteps(),
		RolloverHour:     req.GetRolloverHour(),
		Timezone:         req.GetTimezone(),
	})
	if err != nil {
		return nil, err
	}
	return &collectionv1.CreateDeckConfigResponse{DeckConfig: deckConfigToProto(dc)}, nil
}

func (h *Handler) UpdateDeckConfig(ctx context.Context, req *collectionv1.UpdateDeckConfigRequest) (*collectionv1.UpdateDeckConfigResponse, error) {
	id, err := identity(ctx)
	if err != nil {
		return nil, err
	}
	dc, err := h.svc.UpdateDeckConfig(ctx, id.UserID, req.GetId(), req.GetExpectedUsn(), DeckConfig{
		Name:             req.GetName(),
		NewPerDay:        req.GetNewPerDay(),
		RevPerDay:        req.GetRevPerDay(),
		DesiredRetention: req.GetDesiredRetention(),
		FSRSWeights:      req.GetFsrsWeights(),
		LearningSteps:    req.GetLearningSteps(),
		RolloverHour:     req.GetRolloverHour(),
		Timezone:         req.GetTimezone(),
	})
	if err != nil {
		return nil, err
	}
	return &collectionv1.UpdateDeckConfigResponse{DeckConfig: deckConfigToProto(dc)}, nil
}

func (h *Handler) DeleteDeckConfig(ctx context.Context, req *collectionv1.DeleteDeckConfigRequest) (*collectionv1.DeleteDeckConfigResponse, error) {
	id, err := identity(ctx)
	if err != nil {
		return nil, err
	}
	if err := h.svc.DeleteDeckConfig(ctx, id.UserID, req.GetId(), req.GetExpectedUsn()); err != nil {
		return nil, err
	}
	return &collectionv1.DeleteDeckConfigResponse{}, nil
}

// ─── FilteredDeckService ─────────────────────────────────────────────────────

func (h *Handler) ListFilteredDecks(ctx context.Context, req *collectionv1.ListFilteredDecksRequest) (*collectionv1.ListFilteredDecksResponse, error) {
	id, err := identity(ctx)
	if err != nil {
		return nil, err
	}
	items, next, err := h.svc.ListFilteredDecks(ctx, id.UserID, pageFromProto(req.GetPageToken(), req.GetPageSize()))
	if err != nil {
		return nil, err
	}
	resp := &collectionv1.ListFilteredDecksResponse{NextPageToken: next}
	for i := range items {
		resp.Items = append(resp.Items, filteredDeckToProto(&items[i]))
	}
	return resp, nil
}

func (h *Handler) CreateFilteredDeck(ctx context.Context, req *collectionv1.CreateFilteredDeckRequest) (*collectionv1.CreateFilteredDeckResponse, error) {
	id, err := identity(ctx)
	if err != nil {
		return nil, err
	}
	fd, err := h.svc.CreateFilteredDeck(ctx, id.UserID, id.ProfileID, req.GetName(), req.GetQuery())
	if err != nil {
		return nil, err
	}
	return &collectionv1.CreateFilteredDeckResponse{FilteredDeck: filteredDeckToProto(fd)}, nil
}

func (h *Handler) DeleteFilteredDeck(ctx context.Context, req *collectionv1.DeleteFilteredDeckRequest) (*collectionv1.DeleteFilteredDeckResponse, error) {
	id, err := identity(ctx)
	if err != nil {
		return nil, err
	}
	if err := h.svc.DeleteFilteredDeck(ctx, id.UserID, req.GetId()); err != nil {
		return nil, err
	}
	return &collectionv1.DeleteFilteredDeckResponse{}, nil
}

// ─── CollectionInternalService ───────────────────────────────────────────────

// InternalServicePolicy lists the methods callable only with a service token: the indexer has no user request to forward.
func InternalServicePolicy() auth.ServicePolicy {
	return auth.ServicePolicy{
		collectionv1.CollectionInternalService_GetSearchDocs_FullMethodName: {"search"},
		collectionv1.CollectionInternalService_ListCardIDs_FullMethodName:   {"search"},
	}
}

// Identity comes from the caller's user token; policy methods take it from the request's user_id.

func (h *InternalHandler) GetCardsByIDs(ctx context.Context, req *collectionv1.GetCardsByIDsRequest) (*collectionv1.GetCardsByIDsResponse, error) {
	id, err := identity(ctx)
	if err != nil {
		return nil, err
	}
	cards, err := h.svc.GetCardsByIDs(ctx, id.UserID, req.GetIds())
	if err != nil {
		return nil, err
	}
	resp := &collectionv1.GetCardsByIDsResponse{}
	for i := range cards {
		resp.Cards = append(resp.Cards, cardToProto(&cards[i]))
	}
	return resp, nil
}

func (h *InternalHandler) GetNote(ctx context.Context, req *collectionv1.CollectionInternalServiceGetNoteRequest) (*collectionv1.CollectionInternalServiceGetNoteResponse, error) {
	id, err := identity(ctx)
	if err != nil {
		return nil, err
	}
	n, err := h.svc.GetNote(ctx, id.UserID, req.GetId())
	if err != nil {
		return nil, err
	}
	return &collectionv1.CollectionInternalServiceGetNoteResponse{Note: noteToProto(n)}, nil
}

func (h *InternalHandler) RenderCards(ctx context.Context, req *collectionv1.RenderCardsRequest) (*collectionv1.RenderCardsResponse, error) {
	id, err := identity(ctx)
	if err != nil {
		return nil, err
	}
	rendered, err := h.svc.RenderCards(ctx, id.UserID, req.GetCardIds())
	if err != nil {
		return nil, err
	}
	resp := &collectionv1.RenderCardsResponse{Cards: make([]*collectionv1.RenderedCard, len(rendered))}
	for i, r := range rendered {
		resp.Cards[i] = &collectionv1.RenderedCard{CardId: r.CardID, FrontHtml: r.Front}
	}
	return resp, nil
}

func (h *InternalHandler) ListCardIDs(ctx context.Context, req *collectionv1.ListCardIDsRequest) (*collectionv1.ListCardIDsResponse, error) {
	id, err := identity(ctx)
	if err != nil {
		return nil, err
	}
	if req.GetQuery() != "" {
		return nil, platformerrors.Unimplemented("query-based card listing requires the Search service")
	}
	cardIDs, next, err := h.svc.ListCardIDs(ctx, id.UserID, pageFromProto(req.GetPageToken(), req.GetPageSize()))
	if err != nil {
		return nil, err
	}
	return &collectionv1.ListCardIDsResponse{Ids: cardIDs, NextPageToken: next}, nil
}

func (h *InternalHandler) GetSearchDocs(ctx context.Context, req *collectionv1.GetSearchDocsRequest) (*collectionv1.GetSearchDocsResponse, error) {
	id, err := identity(ctx)
	if err != nil {
		return nil, err
	}
	docs, next, err := h.svc.GetSearchDocs(ctx, id.UserID, SearchDocSelector{
		CardIDs: req.GetCardIds(),
		NoteIDs: req.GetNoteIds(),
		DeckIDs: req.GetDeckIds(),
	}, pageFromProto(req.GetPageToken(), req.GetPageSize()))
	if err != nil {
		return nil, err
	}
	resp := &collectionv1.GetSearchDocsResponse{NextPageToken: next}
	for i := range docs {
		resp.Docs = append(resp.Docs, searchDocToProto(&docs[i]))
	}
	return resp, nil
}

func searchDocToProto(d *SearchDoc) *collectionv1.SearchDoc {
	p := &collectionv1.SearchDoc{
		CardId:      d.CardID,
		UserId:      d.UserID,
		ProfileId:   d.ProfileID,
		NoteId:      d.NoteID,
		DeckId:      d.DeckID,
		DeckPath:    d.DeckPath,
		TemplateOrd: int32(d.TemplateOrd),
		State:       cardStateToProto(d.State),
		Suspended:   d.Suspended,
		Ivl:         int32(d.Ivl),
		Reps:        int32(d.Reps),
		Lapses:      int32(d.Lapses),
		Flags:       d.Flags,
		Tags:        d.Tags,
		FieldsText:  d.FieldsText,
		LangText:    d.LangText,
		AddedAt:     timestamppb.New(d.AddedAt),
		Version:     d.Version,
	}
	if d.Due != nil {
		p.Due = timestamppb.New(*d.Due)
	}
	return p
}
