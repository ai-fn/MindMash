package syncer

import (
	"context"
	"net/http"
	"time"

	"go.uber.org/zap"
	"google.golang.org/protobuf/types/known/timestamppb"

	syncv1 "github.com/mindmesh/mindmesh/api/sync/v1"
	"github.com/mindmesh/mindmesh/internal/platform/auth"
)

// Handler implements the SyncService gRPC interface.
type Handler struct {
	syncv1.UnimplementedSyncServiceServer
	svc       *Service
	heartbeat time.Duration
}

const (
	// sseHeartbeat stays under the ~60s idle timeout of common proxies and load balancers.
	sseHeartbeat = 25 * time.Second
	// sseWriteTimeout bounds each SSE write, replacing the server-wide WriteTimeout for the stream.
	sseWriteTimeout = 10 * time.Second
)

// NewHandler constructs a Handler.
func NewHandler(svc *Service) *Handler {
	return &Handler{svc: svc, heartbeat: sseHeartbeat}
}

func identity(ctx context.Context) (auth.IdentityContext, error) {
	return auth.RequireIdentity(ctx)
}

// ─── Proto ↔ domain mappers ───────────────────────────────────────────────────

func fieldToProto(f Field) *syncv1.Field {
	return &syncv1.Field{Name: f.Name, Sticky: f.Sticky, Rtl: f.RTL, Language: f.Language}
}

func cardTemplateToProto(ct CardTemplate) *syncv1.CardTemplate {
	return &syncv1.CardTemplate{Name: ct.Name, Front: ct.Front, Back: ct.Back}
}

func noteTypeToProto(nt NoteType) *syncv1.NoteType {
	p := &syncv1.NoteType{
		Id:    nt.ID,
		Name:  nt.Name,
		Css:   nt.CSS,
		Usn:   nt.USN,
		Mtime: timestamppb.New(nt.Mtime),
	}
	for _, f := range nt.Fields {
		p.Fields = append(p.Fields, fieldToProto(f))
	}
	for _, ct := range nt.CardTemplates {
		p.CardTemplates = append(p.CardTemplates, cardTemplateToProto(ct))
	}
	return p
}

func noteToProto(n Note) *syncv1.Note {
	return &syncv1.Note{
		Id:         n.ID,
		NoteTypeId: n.NoteTypeID,
		Fields:     n.Fields,
		Tags:       n.Tags,
		Flags:      n.Flags,
		Usn:        n.USN,
		Mtime:      timestamppb.New(n.Mtime),
	}
}

func cardToProto(c Card) *syncv1.Card {
	p := &syncv1.Card{
		Id:          c.ID,
		NoteId:      c.NoteID,
		DeckId:      c.DeckID,
		TemplateOrd: int32(c.TemplateOrd),
		State:       int32(c.State),
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

func deckToProto(d Deck) *syncv1.Deck {
	p := &syncv1.Deck{
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

func deckConfigToProto(dc DeckConfig) *syncv1.DeckConfig {
	return &syncv1.DeckConfig{
		Id:               dc.ID,
		Name:             dc.Name,
		NewPerDay:        dc.NewPerDay,
		RevPerDay:        dc.RevPerDay,
		DesiredRetention: dc.DesiredRetention,
		FsrsWeights:      dc.FSRSWeights,
		RolloverHour:     dc.RolloverHour,
		Timezone:         dc.Timezone,
		MaxIntervalDays:  dc.MaxIntervalDays,
		Usn:              dc.USN,
		Mtime:            timestamppb.New(dc.Mtime),
	}
}

func filteredDeckToProto(fd FilteredDeck) *syncv1.FilteredDeck {
	return &syncv1.FilteredDeck{
		Id:    fd.ID,
		Name:  fd.Name,
		Query: fd.Query,
		Usn:   fd.USN,
		Mtime: timestamppb.New(fd.Mtime),
	}
}

func tagToProto(t Tag) *syncv1.Tag {
	return &syncv1.Tag{
		Id:    t.ID,
		Name:  t.Name,
		Usn:   t.USN,
		Mtime: timestamppb.New(t.Mtime),
	}
}

func tombstoneToProto(t Tombstone) *syncv1.Tombstone {
	return &syncv1.Tombstone{
		Type:      t.Type,
		Id:        t.ID,
		Usn:       t.USN,
		DeletedAt: timestamppb.New(t.DeletedAt),
	}
}

// ─── Apply (push) path proto ↔ domain mappers ─────────────────────────────────

func fieldFromProto(f *syncv1.Field) Field {
	return Field{Name: f.GetName(), Sticky: f.GetSticky(), RTL: f.GetRtl(), Language: f.GetLanguage()}
}

func cardTemplateFromProto(ct *syncv1.CardTemplate) CardTemplate {
	return CardTemplate{Name: ct.GetName(), Front: ct.GetFront(), Back: ct.GetBack()}
}

func mutationTypeFromProto(t syncv1.MutationType) MutationType {
	switch t {
	case syncv1.MutationType_MUTATION_TYPE_NOTE_TYPE_UPDATE:
		return MutationNoteTypeUpdate
	case syncv1.MutationType_MUTATION_TYPE_NOTE_TYPE_DELETE:
		return MutationNoteTypeDelete
	case syncv1.MutationType_MUTATION_TYPE_NOTE_UPDATE:
		return MutationNoteUpdate
	case syncv1.MutationType_MUTATION_TYPE_NOTE_DELETE:
		return MutationNoteDelete
	case syncv1.MutationType_MUTATION_TYPE_DECK_UPDATE:
		return MutationDeckUpdate
	case syncv1.MutationType_MUTATION_TYPE_DECK_DELETE:
		return MutationDeckDelete
	case syncv1.MutationType_MUTATION_TYPE_DECK_CONFIG_UPDATE:
		return MutationDeckConfigUpdate
	case syncv1.MutationType_MUTATION_TYPE_DECK_CONFIG_DELETE:
		return MutationDeckConfigDelete
	case syncv1.MutationType_MUTATION_TYPE_FILTERED_DECK_DELETE:
		return MutationFilteredDeckDelete
	default:
		return 0
	}
}

func noteTypePayloadFromProto(p *syncv1.NoteTypePayload) *NoteTypePayload {
	if p == nil {
		return nil
	}
	out := &NoteTypePayload{Name: p.GetName(), CSS: p.GetCss()}
	for _, f := range p.GetFields() {
		out.Fields = append(out.Fields, fieldFromProto(f))
	}
	for _, ct := range p.GetCardTemplates() {
		out.CardTemplates = append(out.CardTemplates, cardTemplateFromProto(ct))
	}
	return out
}

func notePayloadFromProto(p *syncv1.NotePayload) *NotePayload {
	if p == nil {
		return nil
	}
	return &NotePayload{Fields: p.GetFields(), Tags: p.GetTags(), Flags: p.GetFlags()}
}

func deckPayloadFromProto(p *syncv1.DeckPayload) *DeckPayload {
	if p == nil {
		return nil
	}
	return &DeckPayload{Name: p.GetName(), ParentID: p.GetParentId(), ConfigID: p.GetConfigId()}
}

func deckConfigPayloadFromProto(p *syncv1.DeckConfigPayload) *DeckConfigPayload {
	if p == nil {
		return nil
	}
	return &DeckConfigPayload{
		Name:             p.GetName(),
		NewPerDay:        p.GetNewPerDay(),
		RevPerDay:        p.GetRevPerDay(),
		DesiredRetention: p.GetDesiredRetention(),
		FSRSWeights:      p.GetFsrsWeights(),
		RolloverHour:     p.GetRolloverHour(),
		Timezone:         p.GetTimezone(),
		MaxIntervalDays:  p.GetMaxIntervalDays(),
	}
}

func mutationFromProto(m *syncv1.Mutation) Mutation {
	out := Mutation{
		OpID:     m.GetOpId(),
		Type:     mutationTypeFromProto(m.GetType()),
		EntityID: m.GetEntityId(),
		BaseUSN:  m.GetBaseUsn(),
	}
	if m.GetClientMtime() != nil {
		out.ClientMtime = m.GetClientMtime().AsTime()
	}
	out.NoteType = noteTypePayloadFromProto(m.GetNoteType())
	out.Note = notePayloadFromProto(m.GetNote())
	out.Deck = deckPayloadFromProto(m.GetDeck())
	out.DeckConfig = deckConfigPayloadFromProto(m.GetDeckConfig())
	return out
}

func applyOutcomeToProto(o ApplyOutcome) syncv1.ApplyOutcome {
	switch o {
	case OutcomeApplied:
		return syncv1.ApplyOutcome_APPLY_OUTCOME_APPLIED
	case OutcomeMerged:
		return syncv1.ApplyOutcome_APPLY_OUTCOME_MERGED
	case OutcomeServerWon:
		return syncv1.ApplyOutcome_APPLY_OUTCOME_SERVER_WON
	case OutcomeReplayed:
		return syncv1.ApplyOutcome_APPLY_OUTCOME_REPLAYED
	case OutcomeFailed:
		return syncv1.ApplyOutcome_APPLY_OUTCOME_FAILED
	default:
		return syncv1.ApplyOutcome_APPLY_OUTCOME_UNSPECIFIED
	}
}

func applyResultToProto(r ApplyResult) *syncv1.ApplyResult {
	p := &syncv1.ApplyResult{
		OpId:    r.OpID,
		Outcome: applyOutcomeToProto(r.Outcome),
		Reason:  r.Reason,
		NewUsn:  r.NewUSN,
	}
	switch {
	case r.NoteType != nil:
		p.Entity = &syncv1.ApplyResult_NoteType{NoteType: noteTypeToProto(*r.NoteType)}
	case r.Note != nil:
		p.Entity = &syncv1.ApplyResult_Note{Note: noteToProto(*r.Note)}
	case r.Deck != nil:
		p.Entity = &syncv1.ApplyResult_Deck{Deck: deckToProto(*r.Deck)}
	case r.DeckConfig != nil:
		p.Entity = &syncv1.ApplyResult_DeckConfig{DeckConfig: deckConfigToProto(*r.DeckConfig)}
	case r.FilteredDeck != nil:
		p.Entity = &syncv1.ApplyResult_FilteredDeck{FilteredDeck: filteredDeckToProto(*r.FilteredDeck)}
	}
	return p
}

// ─── SyncService ──────────────────────────────────────────────────────────────

func (h *Handler) GetChanges(ctx context.Context, req *syncv1.GetChangesRequest) (*syncv1.GetChangesResponse, error) {
	id, err := identity(ctx)
	if err != nil {
		return nil, err
	}

	cs, latestUSN, nextToken, err := h.svc.GetChanges(ctx, id.UserID, req.GetSinceUsn(), req.GetPageToken())
	if err != nil {
		return nil, err
	}

	resp := &syncv1.GetChangesResponse{
		LatestUsn:     latestUSN,
		NextPageToken: nextToken,
	}
	for _, nt := range cs.NoteTypes {
		resp.NoteTypes = append(resp.NoteTypes, noteTypeToProto(nt))
	}
	for _, n := range cs.Notes {
		resp.Notes = append(resp.Notes, noteToProto(n))
	}
	for _, c := range cs.Cards {
		resp.Cards = append(resp.Cards, cardToProto(c))
	}
	for _, d := range cs.Decks {
		resp.Decks = append(resp.Decks, deckToProto(d))
	}
	for _, dc := range cs.DeckConfigs {
		resp.DeckConfigs = append(resp.DeckConfigs, deckConfigToProto(dc))
	}
	for _, fd := range cs.FilteredDecks {
		resp.FilteredDecks = append(resp.FilteredDecks, filteredDeckToProto(fd))
	}
	for _, t := range cs.Tags {
		resp.Tags = append(resp.Tags, tagToProto(t))
	}
	for _, t := range cs.Deleted {
		resp.Deleted = append(resp.Deleted, tombstoneToProto(t))
	}
	return resp, nil
}

func (h *Handler) Apply(ctx context.Context, req *syncv1.ApplyRequest) (*syncv1.ApplyResponse, error) {
	id, err := identity(ctx)
	if err != nil {
		return nil, err
	}

	mutations := make([]Mutation, len(req.GetMutations()))
	for i, m := range req.GetMutations() {
		mutations[i] = mutationFromProto(m)
	}

	results, latestUSN, err := h.svc.Apply(ctx, id.UserID, mutations)
	if err != nil {
		return nil, err
	}

	resp := &syncv1.ApplyResponse{LatestUsn: latestUSN}
	for _, r := range results {
		resp.Results = append(resp.Results, applyResultToProto(r))
	}
	return resp, nil
}

// ─── SSE notifier route ───────────────────────────────────────────────────────

// StreamHandler returns a plain http.HandlerFunc for GET /v1/sync/stream. It
// is registered directly on the httpx mux (not through grpc-gateway — see
// sync.proto's SyncService doc comment for why) and relies on
// auth.HTTPAuthMiddleware having already injected the caller's identity.
func (h *Handler) StreamHandler(log *zap.Logger) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		id, ok := auth.IdentityFromContext(r.Context())
		if !ok {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}

		// Subscribe before committing the response status: if the replica's notify
		// subscription is down, the client should see a clean error rather than a
		// 200 stream that silently never nudges.
		nudges, unsubscribe, err := h.svc.Subscribe(id.UserID)
		if err != nil {
			log.Warn("sync: sse subscribe failed", zap.Error(err))
			w.WriteHeader(http.StatusServiceUnavailable)
			return
		}
		defer unsubscribe()

		rc := http.NewResponseController(w)
		send := func(frame string) error {
			if err := rc.SetWriteDeadline(time.Now().Add(sseWriteTimeout)); err != nil {
				return err
			}
			if _, err := w.Write([]byte(frame)); err != nil {
				return err
			}
			return rc.Flush()
		}

		w.Header().Set("Content-Type", "text/event-stream")
		w.Header().Set("Cache-Control", "no-cache")
		w.Header().Set("Connection", "keep-alive")
		// The ready event tells the client the stream is live and it should do an initial pull.
		if err := send("event: ready\ndata: {}\n\n"); err != nil {
			log.Warn("sync: sse open failed", zap.Error(err))
			return
		}

		heartbeat := time.NewTicker(h.heartbeat)
		defer heartbeat.Stop()
		for {
			var frame string
			select {
			case <-r.Context().Done():
				return
			case <-heartbeat.C:
				frame = ": ping\n\n"
			case _, open := <-nudges:
				if !open {
					return
				}
				frame = "event: changed\ndata: {}\n\n"
			}
			if err := send(frame); err != nil {
				log.Debug("sync: sse write failed, closing", zap.Error(err))
				return
			}
		}
	}
}
