package collection

import (
	"cmp"
	"context"
	"strings"
	"time"

	"go.uber.org/zap"

	"github.com/mindmesh/mindmesh/internal/platform/apperr"
	"github.com/mindmesh/mindmesh/internal/platform/events"
	"github.com/mindmesh/mindmesh/internal/platform/ids"
	"github.com/mindmesh/mindmesh/internal/platform/validate"
	"github.com/mindmesh/mindmesh/pkg/template"
)

// Service orchestrates Collection domain operations between the handler layer and the repository.
type Service struct {
	repo Repository
	log  *zap.Logger
}

func NewService(repo Repository, log *zap.Logger) *Service {
	return &Service{repo: repo, log: log}
}

// ─── NoteType ────────────────────────────────────────────────────────────────

func (s *Service) CreateNoteType(ctx context.Context, userID, profileID, name, css string, fields []Field, templates []CardTemplate) (*NoteType, error) {
	if err := validateNoteType(name, css, fields, templates); err != nil {
		return nil, err
	}
	now := time.Now().UTC()
	nt := &NoteType{
		ID:            ids.NewUUID(),
		UserID:        userID,
		ProfileID:     profileID,
		Name:          name,
		Fields:        fields,
		CardTemplates: templates,
		CSS:           css,
		Mtime:         now,
	}

	if err := s.repo.WithTx(ctx, func(tx Repository) error {
		usn, err := tx.NextUSN(ctx, userID)
		if err != nil {
			return err
		}
		nt.USN = usn
		if err := tx.CreateNoteType(ctx, nt); err != nil {
			return err
		}
		return tx.WriteOutbox(ctx, []events.OutboxEntry{noteTypeEntry(nt, EventNoteTypeCreated)})
	}); err != nil {
		return nil, err
	}
	return nt, nil
}

func (s *Service) GetNoteType(ctx context.Context, userID, id string) (*NoteType, error) {
	if err := validate.ID("id", id); err != nil {
		return nil, err
	}
	return s.repo.GetNoteType(ctx, userID, id)
}

func (s *Service) ListNoteTypes(ctx context.Context, userID string, page Page) ([]NoteType, string, error) {
	if err := validatePage(page); err != nil {
		return nil, "", err
	}
	return s.repo.ListNoteTypes(ctx, userID, page)
}

func (s *Service) UpdateNoteType(ctx context.Context, userID, id string, expectedUSN int64, name, css string, fields []Field, templates []CardTemplate) (*NoteType, error) {
	if err := validate.ID("id", id); err != nil {
		return nil, err
	}
	if err := validateNoteType(name, css, fields, templates); err != nil {
		return nil, err
	}
	var existing *NoteType
	if err := s.repo.WithTx(ctx, func(tx Repository) error {
		var err error
		// Read inside the transaction so the USN check and the subsequent UPDATE
		// are not separated by a window where a concurrent write could commit.
		existing, err = tx.GetNoteType(ctx, userID, id)
		if err != nil {
			return err
		}
		if expectedUSN != 0 && existing.USN != expectedUSN {
			return apperr.Aborted("stale expected_usn; refetch and retry")
		}

		existing.Name = name
		existing.CSS = css
		existing.Fields = fields
		existing.CardTemplates = templates
		existing.Mtime = time.Now().UTC()

		usn, err := tx.NextUSN(ctx, userID)
		if err != nil {
			return err
		}
		existing.USN = usn
		if err := tx.UpdateNoteType(ctx, existing); err != nil {
			return err
		}
		return tx.WriteOutbox(ctx, []events.OutboxEntry{noteTypeEntry(existing, EventNoteTypeUpdated)})
	}); err != nil {
		return nil, err
	}
	return existing, nil
}

func (s *Service) DeleteNoteType(ctx context.Context, userID, id string, expectedUSN int64) error {
	if err := validate.ID("id", id); err != nil {
		return err
	}
	return s.repo.WithTx(ctx, func(tx Repository) error {
		if expectedUSN != 0 {
			existing, err := tx.GetNoteType(ctx, userID, id)
			if err != nil {
				return err
			}
			if existing.USN != expectedUSN {
				return apperr.Aborted("stale expected_usn; refetch and retry")
			}
		}
		usn, err := tx.NextUSN(ctx, userID)
		if err != nil {
			return err
		}
		if err := tx.DeleteNoteType(ctx, userID, id); err != nil {
			return err
		}
		if err := tx.WriteTombstones(ctx, userID, TombstoneNoteType, usn, id); err != nil {
			return err
		}
		return tx.WriteOutbox(ctx, []events.OutboxEntry{{
			ID:            ids.NewUUID(), // outbox.id is UUID, not ULID
			UserID:        userID,
			AggregateType: "note_type",
			AggregateID:   id,
			EventType:     EventNoteTypeDeleted,
			Payload:       mustMarshal(map[string]any{"id": id, "user_id": userID, "usn": usn}),
			CreatedAt:     time.Now().UTC(),
		}})
	})
}

// ─── Note ────────────────────────────────────────────────────────────────────

// errNoCards keeps every note with at least one card, so later edits can inherit its deck.
func errNoCards() error {
	return apperr.InvalidArgument("note must generate at least one card; fill a field its templates use")
}

func (s *Service) CreateNote(ctx context.Context, userID, profileID, noteTypeID, deckID string, fields map[string]string, tags []string, flags int32) (*Note, error) {
	if err := validate.ID("note_type_id", noteTypeID); err != nil {
		return nil, err
	}
	if err := validate.ID("deck_id", deckID); err != nil {
		return nil, err
	}
	if err := validateNoteContent(fields, tags); err != nil {
		return nil, err
	}
	if err := validateFlags(flags); err != nil {
		return nil, err
	}
	now := time.Now().UTC()
	note := &Note{
		ID:         ids.NewUUID(),
		UserID:     userID,
		ProfileID:  profileID,
		NoteTypeID: noteTypeID,
		Fields:     fields,
		Tags:       tags,
		Flags:      flags,
		Mtime:      now,
	}

	if err := s.repo.WithTx(ctx, func(tx Repository) error {
		// Lock first so a concurrent DeleteDeck can't drop the deck after it's checked.
		usn, err := tx.NextUSN(ctx, userID)
		if err != nil {
			return err
		}
		nt, err := tx.GetNoteType(ctx, userID, noteTypeID)
		if err != nil {
			return err
		}
		if _, err := tx.GetDeck(ctx, userID, deckID); err != nil {
			return err
		}
		diff, err := GenerateCardDiff(nt, note, nil, deckID, ids.NewUUID)
		if err != nil {
			return err
		}
		if len(diff.ToCreate) == 0 {
			return errNoCards()
		}

		note.USN = usn
		if err := tx.CreateNote(ctx, note); err != nil {
			return err
		}

		outboxEntries := []events.OutboxEntry{noteEntry(note, EventNoteCreated)}
		for i := range diff.ToCreate {
			c := &diff.ToCreate[i]
			c.USN = usn
			c.Mtime = now
			if err := tx.CreateCard(ctx, c); err != nil {
				return err
			}
			outboxEntries = append(outboxEntries, cardEntry(c, EventCardCreated))
		}

		for _, tag := range tags {
			if err := tx.UpsertTag(ctx, &Tag{
				ID:        ids.NewUUID(),
				UserID:    userID,
				ProfileID: profileID,
				Name:      tag,
				USN:       usn,
				Mtime:     now,
			}); err != nil {
				return err
			}
		}

		return tx.WriteOutbox(ctx, outboxEntries)
	}); err != nil {
		return nil, err
	}
	return note, nil
}

func (s *Service) GetNote(ctx context.Context, userID, id string) (*Note, error) {
	if err := validate.ID("id", id); err != nil {
		return nil, err
	}
	return s.repo.GetNote(ctx, userID, id)
}

func (s *Service) UpdateNote(ctx context.Context, userID, id string, expectedUSN int64, fields map[string]string, tags []string, flags int32) (*Note, error) {
	if err := validate.ID("id", id); err != nil {
		return nil, err
	}
	if err := validateNoteContent(fields, tags); err != nil {
		return nil, err
	}
	if err := validateFlags(flags); err != nil {
		return nil, err
	}
	var existing *Note
	if err := s.repo.WithTx(ctx, func(tx Repository) error {
		// Lock first so the USN check and card diff read committed state.
		usn, err := tx.NextUSN(ctx, userID)
		if err != nil {
			return err
		}
		existing, err = tx.GetNote(ctx, userID, id)
		if err != nil {
			return err
		}
		if expectedUSN != 0 && existing.USN != expectedUSN {
			return apperr.Aborted("stale expected_usn; refetch and retry")
		}

		now := time.Now().UTC()
		existing.Fields = fields
		existing.Tags = tags
		existing.Flags = flags
		existing.Mtime = now

		nt, err := tx.GetNoteType(ctx, userID, existing.NoteTypeID)
		if err != nil {
			return err
		}
		existingCards, err := tx.ListCardsByNote(ctx, userID, id)
		if err != nil {
			return err
		}
		if len(existingCards) == 0 {
			return apperr.Internal("note has no cards")
		}

		// New cards join the deck of the note's first card.
		diff, err := GenerateCardDiff(nt, existing, existingCards, existingCards[0].DeckID, ids.NewUUID)
		if err != nil {
			return err
		}
		if len(existingCards)-len(diff.ToDelete)+len(diff.ToCreate) == 0 {
			return errNoCards()
		}

		existing.USN = usn
		if err := tx.UpdateNote(ctx, existing); err != nil {
			return err
		}

		outboxEntries := []events.OutboxEntry{noteEntry(existing, EventNoteUpdated)}

		for i := range diff.ToCreate {
			c := &diff.ToCreate[i]
			c.USN = usn
			c.Mtime = now
			if err := tx.CreateCard(ctx, c); err != nil {
				return err
			}
			outboxEntries = append(outboxEntries, cardEntry(c, EventCardCreated))
		}
		if len(diff.ToDelete) > 0 {
			deleteIDs := make([]string, len(diff.ToDelete))
			for i := range diff.ToDelete {
				deleteIDs[i] = diff.ToDelete[i].ID
			}
			deleted, err := tx.DeleteCardsByIDs(ctx, userID, deleteIDs)
			if err != nil {
				return err
			}
			deletedIDs := make([]string, len(deleted))
			for i := range deleted {
				deletedIDs[i] = deleted[i].ID
				outboxEntries = append(outboxEntries, cardEntry(&deleted[i], EventCardDeleted))
			}
			if err := tx.WriteTombstones(ctx, userID, TombstoneCard, usn, deletedIDs...); err != nil {
				return err
			}
		}

		for _, tag := range tags {
			if err := tx.UpsertTag(ctx, &Tag{
				ID:        ids.NewUUID(),
				UserID:    userID,
				ProfileID: existing.ProfileID,
				Name:      tag,
				USN:       usn,
				Mtime:     now,
			}); err != nil {
				return err
			}
		}

		return tx.WriteOutbox(ctx, outboxEntries)
	}); err != nil {
		return nil, err
	}
	return existing, nil
}

func (s *Service) DeleteNote(ctx context.Context, userID, id string, expectedUSN int64) error {
	if err := validate.ID("id", id); err != nil {
		return err
	}
	return s.repo.WithTx(ctx, func(tx Repository) error {
		// Lock first so the USN check and the DELETE are atomic.
		usn, err := tx.NextUSN(ctx, userID)
		if err != nil {
			return err
		}
		existing, err := tx.GetNote(ctx, userID, id)
		if err != nil {
			return err
		}
		if expectedUSN != 0 && existing.USN != expectedUSN {
			return apperr.Aborted("stale expected_usn; refetch and retry")
		}

		cards, err := tx.DeleteCardsByNote(ctx, userID, id)
		if err != nil {
			return err
		}
		outboxEntries := make([]events.OutboxEntry, 0, len(cards)+1)
		cardIDs := make([]string, len(cards))
		for i := range cards {
			cardIDs[i] = cards[i].ID
			outboxEntries = append(outboxEntries, cardEntry(&cards[i], EventCardDeleted))
		}
		if err := tx.WriteTombstones(ctx, userID, TombstoneCard, usn, cardIDs...); err != nil {
			return err
		}
		if err := tx.DeleteNote(ctx, userID, id); err != nil {
			return err
		}
		if err := tx.WriteTombstones(ctx, userID, TombstoneNote, usn, id); err != nil {
			return err
		}
		outboxEntries = append(outboxEntries, noteEntry(existing, EventNoteDeleted))
		return tx.WriteOutbox(ctx, outboxEntries)
	})
}

// ─── Card ────────────────────────────────────────────────────────────────────

// RenderCard renders a card on every call: note and note-type edits don't bump the card USN,
// so a render cache keyed by it would serve stale content.
func (s *Service) RenderCard(ctx context.Context, userID, cardID string) (card *Card, front, back string, err error) {
	if err := validate.ID("card_id", cardID); err != nil {
		return nil, "", "", err
	}
	src, err := s.repo.GetCardRenderSource(ctx, userID, cardID)
	if err != nil {
		return nil, "", "", err
	}
	front, tmpl, clozeOrd, err := renderFront(src)
	if err != nil {
		return nil, "", "", err
	}
	back, err = template.Render(tmpl.Back, src.Fields, template.Opts{ClozeOrd: clozeOrd, FrontSide: front, Back: true})
	if err != nil {
		return nil, "", "", apperr.Internal("render back", err)
	}
	return src.Card, front, back, nil
}

// RenderCards renders the fronts of the caller's cards among cardIDs, skipping ids that don't exist.
func (s *Service) RenderCards(ctx context.Context, userID string, cardIDs []string) ([]RenderedFront, error) {
	if err := validate.IDs("card_ids", cardIDs); err != nil {
		return nil, err
	}
	srcs, err := s.repo.ListCardRenderSources(ctx, userID, cardIDs)
	if err != nil {
		return nil, err
	}
	out := make([]RenderedFront, len(srcs))
	for i := range srcs {
		front, _, _, err := renderFront(&srcs[i])
		if err != nil {
			return nil, err
		}
		out[i] = RenderedFront{CardID: srcs[i].Card.ID, Front: front}
	}
	return out, nil
}

// renderFront also returns the template and cloze ordinal the back render needs.
func renderFront(src *CardRenderSource) (front string, tmpl CardTemplate, clozeOrd int, err error) {
	var tmplIdx int
	if isCloze(src.Templates) {
		clozeOrd = src.Card.TemplateOrd + 1
	} else {
		tmplIdx = src.Card.TemplateOrd
	}
	if tmplIdx >= len(src.Templates) {
		return "", CardTemplate{}, 0, apperr.Internal("template ordinal out of range")
	}
	tmpl = src.Templates[tmplIdx]

	front, err = template.Render(tmpl.Front, src.Fields, template.Opts{ClozeOrd: clozeOrd})
	if err != nil {
		return "", CardTemplate{}, 0, apperr.Internal("render front", err)
	}
	return front, tmpl, clozeOrd, nil
}

func (s *Service) GetCardsByIDs(ctx context.Context, userID string, cardIDs []string) ([]Card, error) {
	if err := validate.IDs("ids", cardIDs); err != nil {
		return nil, err
	}
	return s.repo.GetCardsByIDs(ctx, userID, cardIDs)
}

func (s *Service) ListCardIDs(ctx context.Context, userID string, page Page) ([]string, string, error) {
	if err := validatePage(page); err != nil {
		return nil, "", err
	}
	return s.repo.ListCardIDs(ctx, userID, page)
}

func (s *Service) GetSearchDocs(ctx context.Context, userID string, sel SearchDocSelector, page Page) ([]SearchDoc, string, error) {
	if err := validateSelector(sel); err != nil {
		return nil, "", err
	}
	if err := validatePage(page); err != nil {
		return nil, "", err
	}
	return s.repo.GetSearchDocs(ctx, userID, sel, page)
}

// ─── Bulk card operations ────────────────────────────────────────────────────

func (s *Service) MoveCards(ctx context.Context, userID string, cardIDs []string, deckID string) (int64, error) {
	if err := validate.ID("target_deck_id", deckID); err != nil {
		return 0, err
	}
	if err := validate.IDs("ids", cardIDs); err != nil {
		return 0, err
	}
	var affected int64
	if err := s.repo.WithTx(ctx, func(tx Repository) error {
		usn, err := tx.NextUSN(ctx, userID)
		if err != nil {
			return err
		}
		updatedIDs, err := tx.MoveCards(ctx, userID, cardIDs, deckID, usn)
		if err != nil {
			return err
		}
		affected = int64(len(updatedIDs))
		if len(updatedIDs) == 0 {
			return nil
		}
		outboxEntries := make([]events.OutboxEntry, 0, len(updatedIDs))
		now := time.Now().UTC()
		for _, cid := range updatedIDs {
			outboxEntries = append(outboxEntries, events.OutboxEntry{
				ID:            ids.NewUUID(), // outbox.id is UUID, not ULID
				UserID:        userID,
				AggregateType: "card",
				AggregateID:   cid,
				EventType:     EventCardUpdated,
				Payload:       mustMarshal(map[string]any{"id": cid, "user_id": userID, "deck_id": deckID, "usn": usn}),
				CreatedAt:     now,
			})
		}
		return tx.WriteOutbox(ctx, outboxEntries)
	}); err != nil {
		return 0, err
	}
	return affected, nil
}

func (s *Service) SuspendCards(ctx context.Context, userID string, cardIDs []string) (int64, error) {
	if err := validate.IDs("ids", cardIDs); err != nil {
		return 0, err
	}
	var affected int64
	if err := s.repo.WithTx(ctx, func(tx Repository) error {
		usn, err := tx.NextUSN(ctx, userID)
		if err != nil {
			return err
		}
		updatedIDs, err := tx.SuspendCards(ctx, userID, cardIDs, usn)
		if err != nil {
			return err
		}
		affected = int64(len(updatedIDs))
		if len(updatedIDs) == 0 {
			return nil
		}
		outboxEntries := make([]events.OutboxEntry, 0, len(updatedIDs))
		now := time.Now().UTC()
		for _, cid := range updatedIDs {
			outboxEntries = append(outboxEntries, events.OutboxEntry{
				ID:            ids.NewUUID(), // outbox.id is UUID, not ULID
				UserID:        userID,
				AggregateType: "card",
				AggregateID:   cid,
				EventType:     EventCardUpdated,
				Payload:       mustMarshal(map[string]any{"id": cid, "user_id": userID, "suspended": true, "usn": usn}),
				CreatedAt:     now,
			})
		}
		return tx.WriteOutbox(ctx, outboxEntries)
	}); err != nil {
		return 0, err
	}
	return affected, nil
}

func (s *Service) DeleteCards(ctx context.Context, userID string, cardIDs []string) (int64, error) {
	if err := validate.IDs("ids", cardIDs); err != nil {
		return 0, err
	}
	var affected int64
	if err := s.repo.WithTx(ctx, func(tx Repository) error {
		usn, err := tx.NextUSN(ctx, userID)
		if err != nil {
			return err
		}
		deleted, err := tx.DeleteCardsByIDs(ctx, userID, cardIDs)
		if err != nil {
			return err
		}
		affected = int64(len(deleted))
		outboxEntries := make([]events.OutboxEntry, 0, len(deleted))
		deletedIDs := make([]string, len(deleted))
		seen := make(map[string]bool)
		var noteIDs []string
		for i := range deleted {
			c := &deleted[i]
			deletedIDs[i] = c.ID
			outboxEntries = append(outboxEntries, cardEntry(c, EventCardDeleted))
			if !seen[c.NoteID] {
				seen[c.NoteID] = true
				noteIDs = append(noteIDs, c.NoteID)
			}
		}
		if err := tx.WriteTombstones(ctx, userID, TombstoneCard, usn, deletedIDs...); err != nil {
			return err
		}
		if len(noteIDs) == 0 {
			return nil
		}

		// As in Anki, a note whose last card is deleted goes with it.
		orphans, err := tx.DeleteOrphanNotes(ctx, userID, noteIDs)
		if err != nil {
			return err
		}
		orphanIDs := make([]string, len(orphans))
		for i := range orphans {
			orphanIDs[i] = orphans[i].ID
			orphans[i].USN = usn
			outboxEntries = append(outboxEntries, noteEntry(&orphans[i], EventNoteDeleted))
		}
		if err := tx.WriteTombstones(ctx, userID, TombstoneNote, usn, orphanIDs...); err != nil {
			return err
		}
		return tx.WriteOutbox(ctx, outboxEntries)
	}); err != nil {
		return 0, err
	}
	return affected, nil
}

func (s *Service) AddTagToCards(ctx context.Context, userID string, cardIDs []string, tag string) (int64, error) {
	if err := validate.IDs("ids", cardIDs); err != nil {
		return 0, err
	}
	if err := validateTag("tag", tag); err != nil {
		return 0, err
	}
	var affected int64
	if err := s.repo.WithTx(ctx, func(tx Repository) error {
		usn, err := tx.NextUSN(ctx, userID)
		if err != nil {
			return err
		}
		noteIDs, err := tx.AddTagToNotesOfCards(ctx, userID, cardIDs, tag, usn)
		if err != nil {
			return err
		}
		affected = int64(len(noteIDs))
		if len(noteIDs) == 0 {
			return nil
		}
		now := time.Now().UTC()
		outboxEntries := make([]events.OutboxEntry, 0, len(noteIDs))
		for _, nid := range noteIDs {
			outboxEntries = append(outboxEntries, events.OutboxEntry{
				ID:            ids.NewUUID(), // outbox.id is UUID, not ULID
				UserID:        userID,
				AggregateType: "note",
				AggregateID:   nid,
				EventType:     EventNoteUpdated,
				Payload:       mustMarshal(map[string]any{"id": nid, "user_id": userID, "tag": tag, "op": "add", "usn": usn}),
				CreatedAt:     now,
			})
		}
		return tx.WriteOutbox(ctx, outboxEntries)
	}); err != nil {
		return 0, err
	}
	return affected, nil
}

func (s *Service) RemoveTagFromCards(ctx context.Context, userID string, cardIDs []string, tag string) (int64, error) {
	if err := validate.IDs("ids", cardIDs); err != nil {
		return 0, err
	}
	if err := validateTag("tag", tag); err != nil {
		return 0, err
	}
	var affected int64
	if err := s.repo.WithTx(ctx, func(tx Repository) error {
		usn, err := tx.NextUSN(ctx, userID)
		if err != nil {
			return err
		}
		noteIDs, err := tx.RemoveTagFromNotesOfCards(ctx, userID, cardIDs, tag, usn)
		if err != nil {
			return err
		}
		affected = int64(len(noteIDs))
		if len(noteIDs) == 0 {
			return nil
		}
		now := time.Now().UTC()
		outboxEntries := make([]events.OutboxEntry, 0, len(noteIDs))
		for _, nid := range noteIDs {
			outboxEntries = append(outboxEntries, events.OutboxEntry{
				ID:            ids.NewUUID(), // outbox.id is UUID, not ULID
				UserID:        userID,
				AggregateType: "note",
				AggregateID:   nid,
				EventType:     EventNoteUpdated,
				Payload:       mustMarshal(map[string]any{"id": nid, "user_id": userID, "tag": tag, "op": "remove", "usn": usn}),
				CreatedAt:     now,
			})
		}
		return tx.WriteOutbox(ctx, outboxEntries)
	}); err != nil {
		return 0, err
	}
	return affected, nil
}

func (s *Service) SetFlagOnCards(ctx context.Context, userID string, cardIDs []string, flag int32) (int64, error) {
	if err := validateFlags(flag); err != nil {
		return 0, err
	}
	if err := validate.IDs("ids", cardIDs); err != nil {
		return 0, err
	}
	var affected int64
	if err := s.repo.WithTx(ctx, func(tx Repository) error {
		usn, err := tx.NextUSN(ctx, userID)
		if err != nil {
			return err
		}
		noteIDs, err := tx.SetFlagOnNotesOfCards(ctx, userID, cardIDs, flag, usn)
		if err != nil {
			return err
		}
		affected = int64(len(noteIDs))
		if len(noteIDs) == 0 {
			return nil
		}
		now := time.Now().UTC()
		outboxEntries := make([]events.OutboxEntry, 0, len(noteIDs))
		for _, nid := range noteIDs {
			outboxEntries = append(outboxEntries, events.OutboxEntry{
				ID:            ids.NewUUID(), // outbox.id is UUID, not ULID
				UserID:        userID,
				AggregateType: "note",
				AggregateID:   nid,
				EventType:     EventNoteUpdated,
				Payload:       mustMarshal(map[string]any{"id": nid, "user_id": userID, "flag": flag, "usn": usn}),
				CreatedAt:     now,
			})
		}
		return tx.WriteOutbox(ctx, outboxEntries)
	}); err != nil {
		return 0, err
	}
	return affected, nil
}

// ─── Deck ────────────────────────────────────────────────────────────────────

// deckLabel strips hyphens from a UUID so it is a valid ltree label.
func deckLabel(id string) string {
	return strings.ReplaceAll(id, "-", "")
}

// deckPath computes the ltree path for a deck given its ID and its parent's path.
func deckPath(id, parentPath string) string {
	label := deckLabel(id)
	if parentPath == "" {
		return label
	}
	return parentPath + "." + label
}

func (s *Service) CreateDeck(ctx context.Context, userID, profileID string, f DeckFields) (*Deck, error) {
	name, parentID, configID := f.Name, f.ParentID, f.ConfigID
	if err := validateDeck(name, parentID, configID); err != nil {
		return nil, err
	}
	now := time.Now().UTC()
	deck := &Deck{
		ID:        ids.NewUUID(),
		UserID:    userID,
		ProfileID: profileID,
		Name:      name,
		Mtime:     now,
	}
	if parentID != "" {
		deck.ParentID = &parentID
	}
	if configID != "" {
		deck.ConfigID = &configID
	}

	if err := s.repo.WithTx(ctx, func(tx Repository) error {
		// Lock before reading the parent so a concurrent move can't leave this child with a stale path.
		usn, err := tx.NextUSN(ctx, userID)
		if err != nil {
			return err
		}
		deck.USN = usn

		parentPath := ""
		if parentID != "" {
			parent, err := tx.GetDeck(ctx, userID, parentID)
			if err != nil {
				return err
			}
			parentPath = parent.Path
		}
		deck.Path = deckPath(deck.ID, parentPath)
		if err := tx.CreateDeck(ctx, deck); err != nil {
			return err
		}
		return tx.WriteOutbox(ctx, []events.OutboxEntry{deckEntry(deck, EventDeckCreated)})
	}); err != nil {
		return nil, err
	}
	return deck, nil
}

func (s *Service) GetDeck(ctx context.Context, userID, id string) (*Deck, error) {
	if err := validate.ID("id", id); err != nil {
		return nil, err
	}
	return s.repo.GetDeck(ctx, userID, id)
}

func (s *Service) ListDecks(ctx context.Context, userID string, page Page) ([]Deck, string, error) {
	if err := validatePage(page); err != nil {
		return nil, "", err
	}
	return s.repo.ListDecks(ctx, userID, page)
}

func (s *Service) UpdateDeck(ctx context.Context, userID, id string, expectedUSN int64, f DeckFields) (*Deck, error) {
	name, parentID, configID := f.Name, f.ParentID, f.ConfigID
	if err := validate.ID("id", id); err != nil {
		return nil, err
	}
	if err := validateDeck(name, parentID, configID); err != nil {
		return nil, err
	}
	var updated *Deck
	if err := s.repo.WithTx(ctx, func(tx Repository) error {
		// NextUSN locks the user's USN row first, serialising deck moves so the
		// cycle check and subtree paths below read committed state.
		usn, err := tx.NextUSN(ctx, userID)
		if err != nil {
			return err
		}
		existing, err := tx.GetDeck(ctx, userID, id)
		if err != nil {
			return err
		}
		if expectedUSN != 0 && existing.USN != expectedUSN {
			return apperr.Aborted("stale expected_usn; refetch and retry")
		}
		now := time.Now().UTC()

		existingParentID := ""
		if existing.ParentID != nil {
			existingParentID = *existing.ParentID
		}
		parentChanged := parentID != existingParentID

		newParentPath := ""
		if parentID != "" {
			parent, err := tx.GetDeck(ctx, userID, parentID)
			if err != nil {
				return err
			}
			if parent.Path == existing.Path || strings.HasPrefix(parent.Path, existing.Path+".") {
				return apperr.InvalidArgument("cannot move a deck under itself or its descendant")
			}
			newParentPath = parent.Path
		}
		newPath := deckPath(id, newParentPath)

		var newParentIDPtr *string
		if parentID != "" {
			newParentIDPtr = &parentID
		}
		var newConfigIDPtr *string
		if configID != "" {
			newConfigIDPtr = &configID
		}

		if parentChanged {
			subtree, err := tx.ListSubtreeDecks(ctx, userID, existing.Path)
			if err != nil {
				return err
			}
			oldPrefix := existing.Path
			outboxEntries := make([]events.OutboxEntry, 0, len(subtree))
			for _, d := range subtree {
				d.Path = newPath + d.Path[len(oldPrefix):]
				d.USN = usn
				d.Mtime = now
				if d.ID == id {
					d.Name = name
					d.ParentID = newParentIDPtr
					d.ConfigID = newConfigIDPtr
				}
				tmp := d
				if err := tx.UpdateDeck(ctx, &tmp); err != nil {
					return err
				}
				outboxEntries = append(outboxEntries, deckEntry(&tmp, EventDeckUpdated))
				if tmp.ID == id {
					updated = &tmp
				}
			}
			return tx.WriteOutbox(ctx, outboxEntries)
		}

		existing.Name = name
		existing.ConfigID = newConfigIDPtr
		existing.Path = newPath
		existing.USN = usn
		existing.Mtime = now
		if err := tx.UpdateDeck(ctx, existing); err != nil {
			return err
		}
		updated = existing
		return tx.WriteOutbox(ctx, []events.OutboxEntry{deckEntry(updated, EventDeckUpdated)})
	}); err != nil {
		return nil, err
	}
	return updated, nil
}

// DeleteDeck reparents the deck's children to its parent (or to root), then deletes it.
func (s *Service) DeleteDeck(ctx context.Context, userID, id string, expectedUSN int64) error {
	if err := validate.ID("id", id); err != nil {
		return err
	}
	return s.repo.WithTx(ctx, func(tx Repository) error {
		usn, err := tx.NextUSN(ctx, userID)
		if err != nil {
			return err
		}
		existing, err := tx.GetDeck(ctx, userID, id)
		if err != nil {
			return err
		}
		if expectedUSN != 0 && existing.USN != expectedUSN {
			return apperr.Aborted("stale expected_usn; refetch and retry")
		}

		subtree, err := tx.ListSubtreeDecks(ctx, userID, existing.Path)
		if err != nil {
			return err
		}
		parentPath := strings.TrimSuffix(strings.TrimSuffix(existing.Path, deckLabel(id)), ".")
		now := time.Now().UTC()
		outboxEntries := make([]events.OutboxEntry, 0, len(subtree))
		for _, d := range subtree {
			if d.ID == id {
				continue
			}
			rest := d.Path[len(existing.Path)+1:]
			d.Path = rest
			if parentPath != "" {
				d.Path = parentPath + "." + rest
			}
			if d.ParentID != nil && *d.ParentID == id {
				d.ParentID = existing.ParentID
			}
			d.USN = usn
			d.Mtime = now
			if err := tx.UpdateDeck(ctx, &d); err != nil {
				return err
			}
			outboxEntries = append(outboxEntries, deckEntry(&d, EventDeckUpdated))
		}

		if err := tx.DeleteDeck(ctx, userID, id); err != nil {
			return err
		}
		if err := tx.WriteTombstones(ctx, userID, TombstoneDeck, usn, id); err != nil {
			return err
		}
		outboxEntries = append(outboxEntries, events.OutboxEntry{
			ID:            ids.NewUUID(), // outbox.id is UUID, not ULID
			UserID:        userID,
			AggregateType: "deck",
			AggregateID:   id,
			EventType:     EventDeckDeleted,
			Payload:       mustMarshal(map[string]any{"id": id, "user_id": userID, "usn": usn}),
			CreatedAt:     now,
		})
		return tx.WriteOutbox(ctx, outboxEntries)
	})
}

// ─── DeckConfig ──────────────────────────────────────────────────────────────

func (s *Service) CreateDeckConfig(ctx context.Context, userID, profileID string, req DeckConfig) (*DeckConfig, error) {
	if err := validateDeckConfig(req); err != nil {
		return nil, err
	}
	now := time.Now().UTC()
	dc := req
	dc.MaxIntervalDays = cmp.Or(dc.MaxIntervalDays, maxIntervalDays)
	dc.ID = ids.NewUUID()
	dc.UserID = userID
	dc.ProfileID = profileID
	dc.Mtime = now

	if err := s.repo.WithTx(ctx, func(tx Repository) error {
		usn, err := tx.NextUSN(ctx, userID)
		if err != nil {
			return err
		}
		dc.USN = usn
		if err := tx.CreateDeckConfig(ctx, &dc); err != nil {
			return err
		}
		return tx.WriteOutbox(ctx, []events.OutboxEntry{deckConfigEntry(&dc, EventDeckConfigCreated)})
	}); err != nil {
		return nil, err
	}
	return &dc, nil
}

func (s *Service) GetDeckConfig(ctx context.Context, userID, id string) (*DeckConfig, error) {
	if err := validate.ID("id", id); err != nil {
		return nil, err
	}
	return s.repo.GetDeckConfig(ctx, userID, id)
}

func (s *Service) ListDeckConfigs(ctx context.Context, userID string, page Page) ([]DeckConfig, string, error) {
	if err := validatePage(page); err != nil {
		return nil, "", err
	}
	return s.repo.ListDeckConfigs(ctx, userID, page)
}

func (s *Service) UpdateDeckConfig(ctx context.Context, userID, id string, expectedUSN int64, req DeckConfig) (*DeckConfig, error) {
	if err := validate.ID("id", id); err != nil {
		return nil, err
	}
	if err := validateDeckConfig(req); err != nil {
		return nil, err
	}
	var existing *DeckConfig
	if err := s.repo.WithTx(ctx, func(tx Repository) error {
		var err error
		// Read inside the transaction to close the TOCTOU window.
		existing, err = tx.GetDeckConfig(ctx, userID, id)
		if err != nil {
			return err
		}
		if expectedUSN != 0 && existing.USN != expectedUSN {
			return apperr.Aborted("stale expected_usn; refetch and retry")
		}

		existing.Name = req.Name
		existing.NewPerDay = req.NewPerDay
		existing.RevPerDay = req.RevPerDay
		existing.DesiredRetention = req.DesiredRetention
		existing.FSRSWeights = req.FSRSWeights
		existing.RolloverHour = req.RolloverHour
		existing.Timezone = req.Timezone
		existing.MaxIntervalDays = cmp.Or(req.MaxIntervalDays, maxIntervalDays)
		existing.Mtime = time.Now().UTC()

		usn, err := tx.NextUSN(ctx, userID)
		if err != nil {
			return err
		}
		existing.USN = usn
		if err := tx.UpdateDeckConfig(ctx, existing); err != nil {
			return err
		}
		return tx.WriteOutbox(ctx, []events.OutboxEntry{deckConfigEntry(existing, EventDeckConfigUpdated)})
	}); err != nil {
		return nil, err
	}
	return existing, nil
}

// DeleteDeckConfig resets decks using the config to the default, then deletes it.
func (s *Service) DeleteDeckConfig(ctx context.Context, userID, id string, expectedUSN int64) error {
	if err := validate.ID("id", id); err != nil {
		return err
	}
	return s.repo.WithTx(ctx, func(tx Repository) error {
		usn, err := tx.NextUSN(ctx, userID)
		if err != nil {
			return err
		}
		if expectedUSN != 0 {
			existing, err := tx.GetDeckConfig(ctx, userID, id)
			if err != nil {
				return err
			}
			if existing.USN != expectedUSN {
				return apperr.Aborted("stale expected_usn; refetch and retry")
			}
		}

		decks, err := tx.ListDecksByConfig(ctx, userID, id)
		if err != nil {
			return err
		}
		now := time.Now().UTC()
		outboxEntries := make([]events.OutboxEntry, 0, len(decks)+1)
		for _, d := range decks {
			d.ConfigID = nil
			d.USN = usn
			d.Mtime = now
			if err := tx.UpdateDeck(ctx, &d); err != nil {
				return err
			}
			outboxEntries = append(outboxEntries, deckEntry(&d, EventDeckUpdated))
		}

		if err := tx.DeleteDeckConfig(ctx, userID, id); err != nil {
			return err
		}
		if err := tx.WriteTombstones(ctx, userID, TombstoneDeckConfig, usn, id); err != nil {
			return err
		}
		outboxEntries = append(outboxEntries, events.OutboxEntry{
			ID:            ids.NewUUID(), // outbox.id is UUID, not ULID
			UserID:        userID,
			AggregateType: "deck_config",
			AggregateID:   id,
			EventType:     EventDeckConfigDeleted,
			Payload:       mustMarshal(map[string]any{"id": id, "user_id": userID, "usn": usn}),
			CreatedAt:     now,
		})
		return tx.WriteOutbox(ctx, outboxEntries)
	})
}

// ─── FilteredDeck ────────────────────────────────────────────────────────────

func (s *Service) CreateFilteredDeck(ctx context.Context, userID, profileID, name, query string) (*FilteredDeck, error) {
	if err := validate.Name("name", name, maxNameRunes); err != nil {
		return nil, err
	}
	if err := validate.Text("query", query, validate.MaxQueryBytes); err != nil {
		return nil, err
	}
	now := time.Now().UTC()
	fd := &FilteredDeck{
		ID:        ids.NewUUID(),
		UserID:    userID,
		ProfileID: profileID,
		Name:      name,
		Query:     query,
		Mtime:     now,
	}

	if err := s.repo.WithTx(ctx, func(tx Repository) error {
		usn, err := tx.NextUSN(ctx, userID)
		if err != nil {
			return err
		}
		fd.USN = usn
		if err := tx.CreateFilteredDeck(ctx, fd); err != nil {
			return err
		}
		return tx.WriteOutbox(ctx, []events.OutboxEntry{filteredDeckEntry(fd, EventFilteredDeckCreated)})
	}); err != nil {
		return nil, err
	}
	return fd, nil
}

func (s *Service) GetFilteredDeck(ctx context.Context, userID, id string) (*FilteredDeck, error) {
	if err := validate.ID("id", id); err != nil {
		return nil, err
	}
	return s.repo.GetFilteredDeck(ctx, userID, id)
}

func (s *Service) ListFilteredDecks(ctx context.Context, userID string, page Page) ([]FilteredDeck, string, error) {
	if err := validatePage(page); err != nil {
		return nil, "", err
	}
	return s.repo.ListFilteredDecks(ctx, userID, page)
}

func (s *Service) DeleteFilteredDeck(ctx context.Context, userID, id string) error {
	if err := validate.ID("id", id); err != nil {
		return err
	}
	return s.repo.WithTx(ctx, func(tx Repository) error {
		usn, err := tx.NextUSN(ctx, userID)
		if err != nil {
			return err
		}
		if err := tx.DeleteFilteredDeck(ctx, userID, id); err != nil {
			return err
		}
		if err := tx.WriteTombstones(ctx, userID, TombstoneFilteredDeck, usn, id); err != nil {
			return err
		}
		return tx.WriteOutbox(ctx, []events.OutboxEntry{{
			ID:            ids.NewUUID(), // outbox.id is UUID, not ULID
			UserID:        userID,
			AggregateType: "filtered_deck",
			AggregateID:   id,
			EventType:     EventFilteredDeckDeleted,
			Payload:       mustMarshal(map[string]any{"id": id, "user_id": userID, "usn": usn}),
			CreatedAt:     time.Now().UTC(),
		}})
	})
}
