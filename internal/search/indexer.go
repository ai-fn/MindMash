package search

import (
	"context"
	"encoding/json"
	"fmt"

	"go.uber.org/zap"
	"google.golang.org/grpc"
	"google.golang.org/grpc/metadata"

	collectionv1 "github.com/mindmesh/mindmesh/api/collection/v1"
	"github.com/mindmesh/mindmesh/internal/platform/events"
)

// IndexTopics are the Kafka topics the indexer consumes. The relay publishes
// each outbox row to a topic named after its aggregate_type, so subscribing to
// these three captures every card/note/deck change while ignoring unrelated
// aggregates (note_type, deck_config, filtered_deck).
var IndexTopics = []string{"card", "note", "deck"}

// enricher is the subset of Collection's internal client the indexer needs:
// resolving changed ids back into denormalized search documents, and (for
// backfill) enumerating a tenant's card ids.
type enricher interface {
	GetSearchDocs(ctx context.Context, in *collectionv1.GetSearchDocsRequest, opts ...grpc.CallOption) (*collectionv1.GetSearchDocsResponse, error)
	ListCardIDs(ctx context.Context, in *collectionv1.ListCardIDsRequest, opts ...grpc.CallOption) (*collectionv1.ListCardIDsResponse, error)
}

// backfillBatch bounds how many card ids are resolved per GetSearchDocs call
// during a backfill, matching the ListCardIDs page size.
const backfillBatch = 200

// TokenMinter mints a short-lived access token the indexer presents to
// Collection. The Kafka event has no user request to forward, so the indexer
// signs a token scoped to the event's own user_id/profile_id; Collection then
// scopes GetSearchDocs to that user exactly as for a real request. *auth.Signer
// satisfies this.
type TokenMinter interface {
	SignAccess(userID, profileID, locale string, roles []string) (string, string, error)
}

// Indexer applies Collection change events to the OpenSearch card index. It is
// at-least-once and idempotent: upserts use external_gte versioning and deletes
// tolerate missing docs, so redelivered or out-of-order events are safe.
type Indexer struct {
	repo   *Repo
	coll   enricher
	minter TokenMinter
	log    *zap.Logger
}

// NewIndexer wires an Indexer.
func NewIndexer(repo *Repo, coll enricher, minter TokenMinter, log *zap.Logger) *Indexer {
	return &Indexer{repo: repo, coll: coll, minter: minter, log: log}
}

// eventPayload is the common envelope every card/note/deck outbox payload shares
// (see collection.cardPayload et al.). The indexer needs only the identity and
// tenant fields; the rest is re-fetched fresh via GetSearchDocs.
type eventPayload struct {
	ID        string `json:"id"`
	UserID    string `json:"user_id"`
	ProfileID string `json:"profile_id"`
}

// Handle processes one Kafka message. It is the handler passed to
// events.Consumer.Subscribe: returning nil commits the offset; returning an
// error leaves the message uncommitted for redelivery. A delete or an
// enrichment that resolves to no documents (the entity was removed before the
// indexer caught up) is a successful no-op.
func (ix *Indexer) Handle(ctx context.Context, msg events.Message) error {
	eventType := msg.Headers["event_type"]
	if eventType == "" {
		ix.log.Warn("indexer: message missing event_type header", zap.String("topic", msg.Topic))
		return nil // unprocessable but not retriable; drop it
	}

	var p eventPayload
	if err := json.Unmarshal(msg.Value, &p); err != nil {
		ix.log.Warn("indexer: undecodable payload", zap.String("event_type", eventType), zap.Error(err))
		return nil // poison message; dropping it avoids a redelivery hot-loop
	}
	if p.ID == "" || p.UserID == "" {
		ix.log.Warn("indexer: payload missing id/user_id", zap.String("event_type", eventType))
		return nil
	}

	switch eventType {
	case "card.created", "card.updated":
		return ix.enrichAndUpsert(ctx, p, &collectionv1.GetSearchDocsRequest{CardIds: []string{p.ID}})
	case "card.deleted":
		return ix.repo.Delete(ctx, p.UserID, p.ID)
	case "note.created", "note.updated":
		return ix.enrichAndUpsert(ctx, p, &collectionv1.GetSearchDocsRequest{NoteIds: []string{p.ID}})
	case "note.deleted":
		return ix.repo.DeleteByField(ctx, p.UserID, "note_id", p.ID)
	case "deck.updated":
		// A deck rename changes deck_path for the whole subtree; GetSearchDocs
		// expands deck_ids to descendants so every affected card is reindexed.
		return ix.enrichAndUpsert(ctx, p, &collectionv1.GetSearchDocsRequest{DeckIds: []string{p.ID}})
	case "deck.deleted":
		return ix.repo.DeleteByField(ctx, p.UserID, "deck_id", p.ID)
	case "deck.created":
		// A new deck has no cards yet; its cards arrive via their own events.
		return nil
	default:
		// Other event types (e.g. note_type.*) should not reach the subscribed
		// topics, but ignore them defensively rather than fail the offset.
		return nil
	}
}

// enrichAndUpsert resolves the changed ids into search documents (as the event's
// user) and upserts each one.
func (ix *Indexer) enrichAndUpsert(ctx context.Context, p eventPayload, req *collectionv1.GetSearchDocsRequest) error {
	authCtx, err := ix.authContext(ctx, p)
	if err != nil {
		return err
	}
	resp, err := ix.coll.GetSearchDocs(authCtx, req)
	if err != nil {
		return fmt.Errorf("indexer: get search docs: %w", err)
	}
	for _, d := range resp.GetDocs() {
		if err := ix.repo.Upsert(ctx, protoToDocument(d)); err != nil {
			return err
		}
	}
	return nil
}

// Backfill re-indexes every card of one tenant by paging through the tenant's
// card ids (ListCardIDs) and re-enriching them via GetSearchDocs, so pre-existing
// documents pick up the per-language buckets that only the forward path fills for
// new/edited cards. It is idempotent (upserts use external_gte) and safe to re-run.
func (ix *Indexer) Backfill(ctx context.Context, userID, profileID string) (int, error) {
	p := eventPayload{UserID: userID, ProfileID: profileID}
	authCtx, err := ix.authContext(ctx, p)
	if err != nil {
		return 0, err
	}
	var indexed int
	var pageToken string
	for {
		page, err := ix.coll.ListCardIDs(authCtx, &collectionv1.ListCardIDsRequest{
			PageToken: pageToken,
			PageSize:  backfillBatch,
		})
		if err != nil {
			return indexed, fmt.Errorf("indexer: backfill list card ids: %w", err)
		}
		ids := page.GetIds()
		if len(ids) > 0 {
			resp, err := ix.coll.GetSearchDocs(authCtx, &collectionv1.GetSearchDocsRequest{CardIds: ids})
			if err != nil {
				return indexed, fmt.Errorf("indexer: backfill get search docs: %w", err)
			}
			for _, d := range resp.GetDocs() {
				if err := ix.repo.Upsert(ctx, protoToDocument(d)); err != nil {
					return indexed, err
				}
				indexed++
			}
		}
		pageToken = page.GetNextPageToken()
		if pageToken == "" {
			return indexed, nil
		}
	}
}

// authContext returns ctx with a freshly minted access token for the event's
// user attached as gRPC authorization metadata.
func (ix *Indexer) authContext(ctx context.Context, p eventPayload) (context.Context, error) {
	token, _, err := ix.minter.SignAccess(p.UserID, p.ProfileID, "", nil)
	if err != nil {
		return nil, fmt.Errorf("indexer: mint token: %w", err)
	}
	return metadata.AppendToOutgoingContext(ctx, "authorization", "Bearer "+token), nil
}

// protoToDocument converts a Collection SearchDoc into the index document.
func protoToDocument(d *collectionv1.SearchDoc) Document {
	doc := Document{
		CardID:      d.GetCardId(),
		UserID:      d.GetUserId(),
		ProfileID:   d.GetProfileId(),
		NoteID:      d.GetNoteId(),
		DeckID:      d.GetDeckId(),
		DeckPath:    d.GetDeckPath(),
		TemplateOrd: d.GetTemplateOrd(),
		State:       cardStateString(d.GetState()),
		Suspended:   d.GetSuspended(),
		Ivl:         d.GetIvl(),
		Reps:        d.GetReps(),
		Lapses:      d.GetLapses(),
		Flags:       d.GetFlags(),
		Tags:        d.GetTags(),
		FieldsText:  d.GetFieldsText(),
		Version:     d.GetVersion(),
	}
	// Route each language bucket to its analyzer-specific field. Languages outside
	// the indexed set (en/ru/zh) are ignored here — their text is still searchable
	// via the agnostic fields_text.
	lang := d.GetLangText()
	doc.FieldsTextEn = lang["en"]
	doc.FieldsTextRu = lang["ru"]
	doc.FieldsTextZh = lang["zh"]
	if d.GetDue() != nil {
		due := d.GetDue().AsTime()
		doc.Due = &due
	}
	if d.GetAddedAt() != nil {
		doc.AddedAt = d.GetAddedAt().AsTime()
	}
	return doc
}

// cardStateString maps the CardState enum to the lowercase token the query
// builder filters on (is:new → state:new).
func cardStateString(s collectionv1.CardState) string {
	switch s {
	case collectionv1.CardState_CARD_STATE_NEW:
		return "new"
	case collectionv1.CardState_CARD_STATE_LEARNING:
		return "learning"
	case collectionv1.CardState_CARD_STATE_REVIEW:
		return "review"
	case collectionv1.CardState_CARD_STATE_RELEARNING:
		return "relearning"
	default:
		return ""
	}
}
