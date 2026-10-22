package sync

import (
	"context"
	"fmt"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	collectionv1 "github.com/mindmesh/mindmesh/api/collection/v1"
)

// Apply resolves and forwards a batch of client-submitted mutations to
// Collection, per /sync/apply's conflict-resolution contract (fast-forward /
// last-write-wins / scheduler-always-wins). Each mutation is applied
// independently — see plan Global Constraint 5 — so one mutation's failure
// never aborts or rolls back its siblings in the same batch; this is a plain
// loop, not a single transaction.
func (s *Service) Apply(ctx context.Context, userID string, mutations []Mutation) ([]ApplyResult, int64, error) {
	results := make([]ApplyResult, len(mutations))

	for i, m := range mutations {
		if prior, err := s.repo.GetAppliedOp(ctx, userID, m.OpID); err != nil {
			return nil, 0, fmt.Errorf("sync: apply: check op_id %s: %w", m.OpID, err)
		} else if prior != nil {
			results[i] = s.replayResult(ctx, m, *prior)
			continue
		}

		res, recordEvenOnFailure := s.dispatchMutation(ctx, userID, m)
		results[i] = res

		if res.Outcome != OutcomeFailed || recordEvenOnFailure {
			if err := s.repo.RecordAppliedOp(ctx, userID, m.OpID, res.Outcome, res.NewUSN); err != nil {
				return nil, 0, fmt.Errorf("sync: apply: record op_id %s: %w", m.OpID, err)
			}
		}
	}

	latestUSN, err := s.repo.LatestUSN(ctx, userID)
	if err != nil {
		return nil, 0, fmt.Errorf("sync: apply: latest usn: %w", err)
	}
	return results, latestUSN, nil
}

// dispatchMutation routes m to the resolver matching its type, returning the
// ApplyResult plus whether to record it even when OutcomeFailed. Per Global
// Constraint 7, a transient codes.Aborted race is never recorded (so a retry
// re-resolves against fresh state); a hard failure (entity not found on an
// update, or any non-NotFound/Aborted transport error) is recorded so a
// replay doesn't keep re-attempting a doomed write.
func (s *Service) dispatchMutation(ctx context.Context, userID string, m Mutation) (ApplyResult, bool) {
	switch m.Type {
	case MutationNoteTypeUpdate:
		return s.applyNoteTypeUpdate(ctx, m)
	case MutationNoteTypeDelete:
		return s.applyNoteTypeDelete(ctx, m)
	case MutationNoteUpdate:
		return s.applyNoteUpdate(ctx, m)
	case MutationNoteDelete:
		return s.applyNoteDelete(ctx, m)
	case MutationDeckUpdate:
		return s.applyDeckUpdate(ctx, m)
	case MutationDeckDelete:
		return s.applyDeckDelete(ctx, m)
	case MutationDeckConfigUpdate:
		return s.applyDeckConfigUpdate(ctx, m)
	case MutationDeckConfigDelete:
		return s.applyDeckConfigDelete(ctx, m)
	case MutationFilteredDeckDelete:
		return s.applyFilteredDeckDelete(ctx, userID, m)
	default:
		return ApplyResult{OpID: m.OpID, Outcome: OutcomeFailed, Reason: "unknown mutation type"}, false
	}
}

// ─── NoteType ────────────────────────────────────────────────────────────────

func protoToNoteType(nt *collectionv1.NoteType) *NoteType {
	if nt == nil {
		return nil
	}
	fields := make([]Field, 0, len(nt.GetFields()))
	for _, f := range nt.GetFields() {
		fields = append(fields, Field{Name: f.GetName(), Sticky: f.GetSticky(), RTL: f.GetRtl(), Language: f.GetLanguage()})
	}
	templates := make([]CardTemplate, 0, len(nt.GetCardTemplates()))
	for _, ct := range nt.GetCardTemplates() {
		templates = append(templates, CardTemplate{Name: ct.GetName(), Front: ct.GetFront(), Back: ct.GetBack()})
	}
	return &NoteType{
		ID:            nt.GetId(),
		Name:          nt.GetName(),
		CSS:           nt.GetCss(),
		Fields:        fields,
		CardTemplates: templates,
		USN:           nt.GetUsn(),
		Mtime:         nt.GetMtime().AsTime(),
	}
}

func (s *Service) applyNoteTypeUpdate(ctx context.Context, m Mutation) (ApplyResult, bool) {
	getResp, err := s.coll.GetNoteType(ctx, &collectionv1.GetNoteTypeRequest{Id: m.EntityID})
	if err != nil {
		if st, ok := status.FromError(err); ok && st.Code() == codes.NotFound {
			return ApplyResult{OpID: m.OpID, Outcome: OutcomeFailed, Reason: "note type not found"}, true
		}
		return ApplyResult{OpID: m.OpID, Outcome: OutcomeFailed, Reason: err.Error()}, false
	}
	server := getResp.GetNoteType()

	if server.GetUsn() != m.BaseUSN && !m.ClientMtime.After(server.GetMtime().AsTime()) {
		return ApplyResult{
			OpID: m.OpID, Outcome: OutcomeServerWon,
			Reason:   "server note type is newer (LWW by mtime)",
			NoteType: protoToNoteType(server),
		}, true
	}

	var fields []*collectionv1.Field
	var templates []*collectionv1.CardTemplate
	var name, css string
	if m.NoteType != nil {
		name, css = m.NoteType.Name, m.NoteType.CSS
		for _, f := range m.NoteType.Fields {
			fields = append(fields, &collectionv1.Field{Name: f.Name, Sticky: f.Sticky, Rtl: f.RTL, Language: f.Language})
		}
		for _, ct := range m.NoteType.CardTemplates {
			templates = append(templates, &collectionv1.CardTemplate{Name: ct.Name, Front: ct.Front, Back: ct.Back})
		}
	}

	updResp, err := s.coll.UpdateNoteType(ctx, &collectionv1.UpdateNoteTypeRequest{
		Id: m.EntityID, Name: name, Fields: fields, CardTemplates: templates, Css: css,
		ExpectedUsn: server.GetUsn(),
	})
	if err != nil {
		if st, ok := status.FromError(err); ok && st.Code() == codes.Aborted {
			return ApplyResult{OpID: m.OpID, Outcome: OutcomeFailed, Reason: "concurrent write race, retry"}, false
		}
		return ApplyResult{OpID: m.OpID, Outcome: OutcomeFailed, Reason: err.Error()}, false
	}

	outcome := OutcomeApplied
	if server.GetUsn() != m.BaseUSN {
		outcome = OutcomeMerged
	}
	return ApplyResult{
		OpID: m.OpID, Outcome: outcome, NewUSN: updResp.GetNoteType().GetUsn(),
		NoteType: protoToNoteType(updResp.GetNoteType()),
	}, true
}

func (s *Service) applyNoteTypeDelete(ctx context.Context, m Mutation) (ApplyResult, bool) {
	getResp, err := s.coll.GetNoteType(ctx, &collectionv1.GetNoteTypeRequest{Id: m.EntityID})
	if err != nil {
		if st, ok := status.FromError(err); ok && st.Code() == codes.NotFound {
			return ApplyResult{OpID: m.OpID, Outcome: OutcomeApplied, Reason: "already deleted"}, true
		}
		return ApplyResult{OpID: m.OpID, Outcome: OutcomeFailed, Reason: err.Error()}, false
	}
	server := getResp.GetNoteType()

	if server.GetUsn() != m.BaseUSN {
		return ApplyResult{
			OpID: m.OpID, Outcome: OutcomeServerWon,
			Reason:   "note type changed on server since base_usn; delete discarded",
			NoteType: protoToNoteType(server),
		}, true
	}

	if _, err := s.coll.DeleteNoteType(ctx, &collectionv1.DeleteNoteTypeRequest{Id: m.EntityID, ExpectedUsn: server.GetUsn()}); err != nil {
		if st, ok := status.FromError(err); ok && st.Code() == codes.Aborted {
			return ApplyResult{OpID: m.OpID, Outcome: OutcomeFailed, Reason: "concurrent write race, retry"}, false
		}
		return ApplyResult{OpID: m.OpID, Outcome: OutcomeFailed, Reason: err.Error()}, false
	}
	return ApplyResult{OpID: m.OpID, Outcome: OutcomeApplied}, true
}

// ─── Note ────────────────────────────────────────────────────────────────────

func protoToNote(n *collectionv1.Note) *Note {
	if n == nil {
		return nil
	}
	return &Note{
		ID:         n.GetId(),
		NoteTypeID: n.GetNoteTypeId(),
		Fields:     n.GetFields(),
		Tags:       n.GetTags(),
		Flags:      n.GetFlags(),
		USN:        n.GetUsn(),
		Mtime:      n.GetMtime().AsTime(),
	}
}

func (s *Service) applyNoteUpdate(ctx context.Context, m Mutation) (ApplyResult, bool) {
	getResp, err := s.coll.GetNote(ctx, &collectionv1.GetNoteRequest{Id: m.EntityID})
	if err != nil {
		if st, ok := status.FromError(err); ok && st.Code() == codes.NotFound {
			return ApplyResult{OpID: m.OpID, Outcome: OutcomeFailed, Reason: "note not found"}, true
		}
		return ApplyResult{OpID: m.OpID, Outcome: OutcomeFailed, Reason: err.Error()}, false
	}
	server := getResp.GetNote()

	if server.GetUsn() != m.BaseUSN && !m.ClientMtime.After(server.GetMtime().AsTime()) {
		return ApplyResult{
			OpID: m.OpID, Outcome: OutcomeServerWon,
			Reason: "server note is newer (LWW by mtime)",
			Note:   protoToNote(server),
		}, true
	}

	var fields map[string]string
	var tags []string
	var flags int32
	if m.Note != nil {
		fields, tags, flags = m.Note.Fields, m.Note.Tags, m.Note.Flags
	}

	updResp, err := s.coll.UpdateNote(ctx, &collectionv1.UpdateNoteRequest{
		Id: m.EntityID, Fields: fields, Tags: tags, Flags: flags,
		ExpectedUsn: server.GetUsn(),
	})
	if err != nil {
		if st, ok := status.FromError(err); ok && st.Code() == codes.Aborted {
			return ApplyResult{OpID: m.OpID, Outcome: OutcomeFailed, Reason: "concurrent write race, retry"}, false
		}
		return ApplyResult{OpID: m.OpID, Outcome: OutcomeFailed, Reason: err.Error()}, false
	}

	outcome := OutcomeApplied
	if server.GetUsn() != m.BaseUSN {
		outcome = OutcomeMerged
	}
	return ApplyResult{OpID: m.OpID, Outcome: outcome, NewUSN: updResp.GetNote().GetUsn(), Note: protoToNote(updResp.GetNote())}, true
}

func (s *Service) applyNoteDelete(ctx context.Context, m Mutation) (ApplyResult, bool) {
	getResp, err := s.coll.GetNote(ctx, &collectionv1.GetNoteRequest{Id: m.EntityID})
	if err != nil {
		if st, ok := status.FromError(err); ok && st.Code() == codes.NotFound {
			return ApplyResult{OpID: m.OpID, Outcome: OutcomeApplied, Reason: "already deleted"}, true
		}
		return ApplyResult{OpID: m.OpID, Outcome: OutcomeFailed, Reason: err.Error()}, false
	}
	server := getResp.GetNote()

	if server.GetUsn() != m.BaseUSN {
		return ApplyResult{
			OpID: m.OpID, Outcome: OutcomeServerWon,
			Reason: "note changed on server since base_usn; delete discarded",
			Note:   protoToNote(server),
		}, true
	}

	if _, err := s.coll.DeleteNote(ctx, &collectionv1.DeleteNoteRequest{Id: m.EntityID, ExpectedUsn: server.GetUsn()}); err != nil {
		if st, ok := status.FromError(err); ok && st.Code() == codes.Aborted {
			return ApplyResult{OpID: m.OpID, Outcome: OutcomeFailed, Reason: "concurrent write race, retry"}, false
		}
		return ApplyResult{OpID: m.OpID, Outcome: OutcomeFailed, Reason: err.Error()}, false
	}
	return ApplyResult{OpID: m.OpID, Outcome: OutcomeApplied}, true
}

// ─── Deck ────────────────────────────────────────────────────────────────────

func protoToDeck(d *collectionv1.Deck) *Deck {
	if d == nil {
		return nil
	}
	var parentID, configID *string
	if v := d.GetParentId(); v != "" {
		parentID = &v
	}
	if v := d.GetConfigId(); v != "" {
		configID = &v
	}
	return &Deck{
		ID:       d.GetId(),
		ParentID: parentID,
		Name:     d.GetName(),
		Path:     d.GetPath(),
		ConfigID: configID,
		USN:      d.GetUsn(),
		Mtime:    d.GetMtime().AsTime(),
	}
}

func (s *Service) applyDeckUpdate(ctx context.Context, m Mutation) (ApplyResult, bool) {
	getResp, err := s.coll.GetDeck(ctx, &collectionv1.GetDeckRequest{Id: m.EntityID})
	if err != nil {
		if st, ok := status.FromError(err); ok && st.Code() == codes.NotFound {
			return ApplyResult{OpID: m.OpID, Outcome: OutcomeFailed, Reason: "deck not found"}, true
		}
		return ApplyResult{OpID: m.OpID, Outcome: OutcomeFailed, Reason: err.Error()}, false
	}
	server := getResp.GetDeck()

	if server.GetUsn() != m.BaseUSN && !m.ClientMtime.After(server.GetMtime().AsTime()) {
		return ApplyResult{
			OpID: m.OpID, Outcome: OutcomeServerWon,
			Reason: "server deck is newer (LWW by mtime)",
			Deck:   protoToDeck(server),
		}, true
	}

	var name, parentID, configID string
	if m.Deck != nil {
		name, parentID, configID = m.Deck.Name, m.Deck.ParentID, m.Deck.ConfigID
	}

	updResp, err := s.coll.UpdateDeck(ctx, &collectionv1.UpdateDeckRequest{
		Id: m.EntityID, Name: name, ParentId: parentID, ConfigId: configID,
		ExpectedUsn: server.GetUsn(),
	})
	if err != nil {
		if st, ok := status.FromError(err); ok && st.Code() == codes.Aborted {
			return ApplyResult{OpID: m.OpID, Outcome: OutcomeFailed, Reason: "concurrent write race, retry"}, false
		}
		return ApplyResult{OpID: m.OpID, Outcome: OutcomeFailed, Reason: err.Error()}, false
	}

	outcome := OutcomeApplied
	if server.GetUsn() != m.BaseUSN {
		outcome = OutcomeMerged
	}
	return ApplyResult{OpID: m.OpID, Outcome: outcome, NewUSN: updResp.GetDeck().GetUsn(), Deck: protoToDeck(updResp.GetDeck())}, true
}

func (s *Service) applyDeckDelete(ctx context.Context, m Mutation) (ApplyResult, bool) {
	getResp, err := s.coll.GetDeck(ctx, &collectionv1.GetDeckRequest{Id: m.EntityID})
	if err != nil {
		if st, ok := status.FromError(err); ok && st.Code() == codes.NotFound {
			return ApplyResult{OpID: m.OpID, Outcome: OutcomeApplied, Reason: "already deleted"}, true
		}
		return ApplyResult{OpID: m.OpID, Outcome: OutcomeFailed, Reason: err.Error()}, false
	}
	server := getResp.GetDeck()

	if server.GetUsn() != m.BaseUSN {
		return ApplyResult{
			OpID: m.OpID, Outcome: OutcomeServerWon,
			Reason: "deck changed on server since base_usn; delete discarded",
			Deck:   protoToDeck(server),
		}, true
	}

	if _, err := s.coll.DeleteDeck(ctx, &collectionv1.DeleteDeckRequest{Id: m.EntityID, ExpectedUsn: server.GetUsn()}); err != nil {
		if st, ok := status.FromError(err); ok && st.Code() == codes.Aborted {
			return ApplyResult{OpID: m.OpID, Outcome: OutcomeFailed, Reason: "concurrent write race, retry"}, false
		}
		return ApplyResult{OpID: m.OpID, Outcome: OutcomeFailed, Reason: err.Error()}, false
	}
	return ApplyResult{OpID: m.OpID, Outcome: OutcomeApplied}, true
}

// ─── DeckConfig ──────────────────────────────────────────────────────────────

func protoToDeckConfig(dc *collectionv1.DeckConfig) *DeckConfig {
	if dc == nil {
		return nil
	}
	return &DeckConfig{
		ID:               dc.GetId(),
		Name:             dc.GetName(),
		NewPerDay:        dc.GetNewPerDay(),
		RevPerDay:        dc.GetRevPerDay(),
		DesiredRetention: dc.GetDesiredRetention(),
		FSRSWeights:      dc.GetFsrsWeights(),
		LearningSteps:    dc.GetLearningSteps(),
		RolloverHour:     dc.GetRolloverHour(),
		Timezone:         dc.GetTimezone(),
		USN:              dc.GetUsn(),
		Mtime:            dc.GetMtime().AsTime(),
	}
}

func (s *Service) applyDeckConfigUpdate(ctx context.Context, m Mutation) (ApplyResult, bool) {
	getResp, err := s.coll.GetDeckConfig(ctx, &collectionv1.GetDeckConfigRequest{Id: m.EntityID})
	if err != nil {
		if st, ok := status.FromError(err); ok && st.Code() == codes.NotFound {
			return ApplyResult{OpID: m.OpID, Outcome: OutcomeFailed, Reason: "deck config not found"}, true
		}
		return ApplyResult{OpID: m.OpID, Outcome: OutcomeFailed, Reason: err.Error()}, false
	}
	server := getResp.GetDeckConfig()

	if server.GetUsn() != m.BaseUSN && !m.ClientMtime.After(server.GetMtime().AsTime()) {
		return ApplyResult{
			OpID: m.OpID, Outcome: OutcomeServerWon,
			Reason:     "server deck config is newer (LWW by mtime)",
			DeckConfig: protoToDeckConfig(server),
		}, true
	}

	var p DeckConfigPayload
	if m.DeckConfig != nil {
		p = *m.DeckConfig
	}

	updResp, err := s.coll.UpdateDeckConfig(ctx, &collectionv1.UpdateDeckConfigRequest{
		Id: m.EntityID, Name: p.Name, NewPerDay: p.NewPerDay, RevPerDay: p.RevPerDay,
		DesiredRetention: p.DesiredRetention, FsrsWeights: p.FSRSWeights,
		LearningSteps: p.LearningSteps, RolloverHour: p.RolloverHour, Timezone: p.Timezone,
		ExpectedUsn: server.GetUsn(),
	})
	if err != nil {
		if st, ok := status.FromError(err); ok && st.Code() == codes.Aborted {
			return ApplyResult{OpID: m.OpID, Outcome: OutcomeFailed, Reason: "concurrent write race, retry"}, false
		}
		return ApplyResult{OpID: m.OpID, Outcome: OutcomeFailed, Reason: err.Error()}, false
	}

	outcome := OutcomeApplied
	if server.GetUsn() != m.BaseUSN {
		outcome = OutcomeMerged
	}
	return ApplyResult{
		OpID: m.OpID, Outcome: outcome, NewUSN: updResp.GetDeckConfig().GetUsn(),
		DeckConfig: protoToDeckConfig(updResp.GetDeckConfig()),
	}, true
}

func (s *Service) applyDeckConfigDelete(ctx context.Context, m Mutation) (ApplyResult, bool) {
	getResp, err := s.coll.GetDeckConfig(ctx, &collectionv1.GetDeckConfigRequest{Id: m.EntityID})
	if err != nil {
		if st, ok := status.FromError(err); ok && st.Code() == codes.NotFound {
			return ApplyResult{OpID: m.OpID, Outcome: OutcomeApplied, Reason: "already deleted"}, true
		}
		return ApplyResult{OpID: m.OpID, Outcome: OutcomeFailed, Reason: err.Error()}, false
	}
	server := getResp.GetDeckConfig()

	if server.GetUsn() != m.BaseUSN {
		return ApplyResult{
			OpID: m.OpID, Outcome: OutcomeServerWon,
			Reason:     "deck config changed on server since base_usn; delete discarded",
			DeckConfig: protoToDeckConfig(server),
		}, true
	}

	if _, err := s.coll.DeleteDeckConfig(ctx, &collectionv1.DeleteDeckConfigRequest{Id: m.EntityID, ExpectedUsn: server.GetUsn()}); err != nil {
		if st, ok := status.FromError(err); ok && st.Code() == codes.Aborted {
			return ApplyResult{OpID: m.OpID, Outcome: OutcomeFailed, Reason: "concurrent write race, retry"}, false
		}
		return ApplyResult{OpID: m.OpID, Outcome: OutcomeFailed, Reason: err.Error()}, false
	}
	return ApplyResult{OpID: m.OpID, Outcome: OutcomeApplied}, true
}

// ─── FilteredDeck ────────────────────────────────────────────────────────────
//
// This resolver provides NO conflict/staleness detection, by construction —
// not by a fixable bug, and not at parity with the other 8 resolvers in this
// file. Two structural facts force that:
//
//  1. Collection's Handler.DeleteFilteredDeck (internal/collection/handler.go)
//     calls Service.DeleteFilteredDeck(ctx, userID, id) — it never reads
//     req.GetExpectedUsn() at all. Service.DeleteFilteredDeck itself
//     (internal/collection/service.go) takes no expected-usn parameter and
//     does no USN comparison anywhere in its body; it unconditionally mints a
//     new USN, deletes the row, and writes the tombstone. So the ExpectedUsn
//     we pass below (m.BaseUSN, the client's stale value) is accepted by the
//     wire format but is inert — Collection silently ignores it server-side.
//  2. There is no GetFilteredDeck RPC, so even if Collection did honor
//     expected_usn, this resolver has no way to read a fresh server-side USN
//     before deciding fast-forward vs LWW vs server-won, the way the other
//     resolvers do. FilteredDecks are also immutable (no Update RPC — edit is
//     delete + recreate, per spec 02's FilteredDeckService), so there is
//     nothing for a delete to race against in practice. Adding a Get RPC to
//     Collection to fix this would violate Global Constraint 1 and is out of
//     scope.
//
// Net effect: this call always succeeds idempotently (NotFound is treated as
// already-deleted success per Global Constraint 8) regardless of whether the
// client's view of the FilteredDeck was stale. codes.Aborted is still handled
// below for forward-compatibility, but Collection never returns it today.
func (s *Service) applyFilteredDeckDelete(ctx context.Context, _ string, m Mutation) (ApplyResult, bool) {
	_, err := s.coll.DeleteFilteredDeck(ctx, &collectionv1.DeleteFilteredDeckRequest{Id: m.EntityID, ExpectedUsn: m.BaseUSN})
	if err != nil {
		if st, ok := status.FromError(err); ok {
			switch st.Code() {
			case codes.NotFound:
				return ApplyResult{OpID: m.OpID, Outcome: OutcomeApplied, Reason: "already deleted"}, true
			case codes.Aborted:
				return ApplyResult{OpID: m.OpID, Outcome: OutcomeFailed, Reason: "concurrent write race, retry"}, false
			}
		}
		return ApplyResult{OpID: m.OpID, Outcome: OutcomeFailed, Reason: err.Error()}, false
	}
	return ApplyResult{OpID: m.OpID, Outcome: OutcomeApplied}, true
}

// ─── Replay ──────────────────────────────────────────────────────────────────

// replayResult handles a Mutation whose op_id was already applied in a prior
// call: per Global Constraint 9, sync_applied_ops stores only outcome/new_usn
// (never a cached entity payload), so this does a best-effort live re-fetch
// of the entity to populate the response. A refresh failure (or an entity
// type with no live state to show, e.g. FilteredDeck deletes) degrades to
// bare metadata — still OutcomeReplayed — rather than failing the whole
// batch, since the client already knows this op_id succeeded previously.
func (s *Service) replayResult(ctx context.Context, m Mutation, prior AppliedOp) ApplyResult {
	res := ApplyResult{OpID: m.OpID, Outcome: OutcomeReplayed, NewUSN: prior.NewUSN}

	switch m.Type {
	case MutationNoteTypeUpdate:
		if resp, err := s.coll.GetNoteType(ctx, &collectionv1.GetNoteTypeRequest{Id: m.EntityID}); err == nil {
			res.NoteType = protoToNoteType(resp.GetNoteType())
		}
	case MutationNoteUpdate:
		if resp, err := s.coll.GetNote(ctx, &collectionv1.GetNoteRequest{Id: m.EntityID}); err == nil {
			res.Note = protoToNote(resp.GetNote())
		}
	case MutationDeckUpdate:
		if resp, err := s.coll.GetDeck(ctx, &collectionv1.GetDeckRequest{Id: m.EntityID}); err == nil {
			res.Deck = protoToDeck(resp.GetDeck())
		}
	case MutationDeckConfigUpdate:
		if resp, err := s.coll.GetDeckConfig(ctx, &collectionv1.GetDeckConfigRequest{Id: m.EntityID}); err == nil {
			res.DeckConfig = protoToDeckConfig(resp.GetDeckConfig())
		}
	// Deletes (NoteType/Note/Deck/DeckConfig/FilteredDeck) and
	// FilteredDeck's update-less lifecycle have no live entity to refresh —
	// the prior outcome's bare metadata is the whole response.
	default:
	}

	return res
}
