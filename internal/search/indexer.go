package search

import (
	"context"
	"encoding/json"
	"fmt"

	"go.uber.org/zap"
	"google.golang.org/grpc"
	"google.golang.org/grpc/metadata"

	collectionv1 "github.com/mindmesh/mindmesh/api/collection/v1"
	"github.com/mindmesh/mindmesh/internal/platform/auth"
	"github.com/mindmesh/mindmesh/internal/platform/events"
	"github.com/mindmesh/mindmesh/internal/platform/validate"
)

// IndexTopics include review_log because answers change a card's state, due and reps.
var IndexTopics = []string{"card", "note", "deck", "review_log"}

// enricher is the subset of Collection's internal client the indexer needs.
type enricher interface {
	GetSearchDocs(ctx context.Context, in *collectionv1.GetSearchDocsRequest, opts ...grpc.CallOption) (*collectionv1.GetSearchDocsResponse, error)
	ListCardIDs(ctx context.Context, in *collectionv1.ListCardIDsRequest, opts ...grpc.CallOption) (*collectionv1.ListCardIDsResponse, error)
}

const (
	// backfillBatch is the ListCardIDs page size during a backfill.
	backfillBatch = 200
	// docsPageSize is the GetSearchDocs page size; Collection also cuts pages at 4 MiB of note text.
	docsPageSize = 200
	// docsRecvBytes leaves headroom over the 4 MiB page cap for per-doc metadata.
	docsRecvBytes = 16 << 20
)

// ServiceTokens signs the indexer's own service tokens; events carry no user request to forward.
type ServiceTokens interface {
	Sign(audience string) (string, error)
}

// Indexer is at-least-once: external_gte upserts and missing-doc-tolerant deletes make redelivered
// or reordered events safe.
type Indexer struct {
	repo     *Repo
	coll     enricher
	tokens   ServiceTokens
	log      *zap.Logger
	pageSize int32
}

// NewIndexer wires an Indexer.
func NewIndexer(repo *Repo, coll enricher, tokens ServiceTokens, log *zap.Logger) *Indexer {
	return &Indexer{repo: repo, coll: coll, tokens: tokens, log: log, pageSize: docsPageSize}
}

// eventPayload keeps only ids; the document is re-fetched fresh via GetSearchDocs.
type eventPayload struct {
	ID        string `json:"id"`
	UserID    string `json:"user_id"`
	ProfileID string `json:"profile_id"`
	CardID    string `json:"card_id"` // set on review.recorded, whose id is the review log
}

// Handle treats an enrichment that finds nothing as success: the entity was deleted meanwhile.
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
	if !validate.IsUUID(p.ID) || !validate.IsUUID(p.UserID) {
		ix.log.Warn("indexer: payload id or user_id is not a uuid", zap.String("event_type", eventType))
		return nil
	}

	switch eventType {
	case "card.created", "card.updated", "card.rescheduled":
		return ix.enrichAndUpsert(ctx, p, &collectionv1.GetSearchDocsRequest{CardIds: []string{p.ID}})
	case "review.recorded":
		if !validate.IsUUID(p.CardID) {
			ix.log.Warn("indexer: review payload card_id is not a uuid", zap.String("user_id", p.UserID), zap.String("review_id", p.ID))
			return nil
		}
		return ix.enrichAndUpsert(ctx, p, &collectionv1.GetSearchDocsRequest{CardIds: []string{p.CardID}})
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
// user) page by page and bulk-upserts each page.
func (ix *Indexer) enrichAndUpsert(ctx context.Context, p eventPayload, req *collectionv1.GetSearchDocsRequest) error {
	req.UserId = p.UserID
	_, err := ix.indexDocs(ctx, req)
	return err
}

// indexDocs pages through GetSearchDocs for req and upserts every page, returning the doc count.
func (ix *Indexer) indexDocs(ctx context.Context, req *collectionv1.GetSearchDocsRequest) (int, error) {
	req.PageSize = ix.pageSize
	req.PageToken = ""
	var indexed int
	for {
		// Re-signed per page: a large subtree outlives one service token.
		authCtx, err := ix.authContext(ctx)
		if err != nil {
			return indexed, err
		}
		resp, err := ix.coll.GetSearchDocs(authCtx, req, grpc.MaxCallRecvMsgSize(docsRecvBytes))
		if err != nil {
			return indexed, fmt.Errorf("indexer: get search docs: %w", err)
		}
		docs := make([]Document, len(resp.GetDocs()))
		for i, d := range resp.GetDocs() {
			docs[i] = protoToDocument(d)
		}
		if err := ix.repo.Upsert(ctx, docs...); err != nil {
			return indexed, err
		}
		indexed += len(docs)
		if req.PageToken = resp.GetNextPageToken(); req.PageToken == "" {
			return indexed, nil
		}
	}
}

// Backfill re-indexes all of a tenant's cards, filling fields the forward path only sets on new
// edits; safe to re-run.
func (ix *Indexer) Backfill(ctx context.Context, userID string) (int, error) {
	if !validate.IsUUID(userID) {
		return 0, fmt.Errorf("indexer: backfill: %q is not a canonical uuid", userID)
	}
	var indexed int
	var pageToken string
	for {
		authCtx, err := ix.authContext(ctx)
		if err != nil {
			return indexed, err
		}
		page, err := ix.coll.ListCardIDs(authCtx, &collectionv1.ListCardIDsRequest{
			UserId:    userID,
			PageToken: pageToken,
			PageSize:  backfillBatch,
		})
		if err != nil {
			return indexed, fmt.Errorf("indexer: backfill list card ids: %w", err)
		}
		if ids := page.GetIds(); len(ids) > 0 {
			n, err := ix.indexDocs(ctx, &collectionv1.GetSearchDocsRequest{UserId: userID, CardIds: ids})
			indexed += n
			if err != nil {
				return indexed, err
			}
		}
		pageToken = page.GetNextPageToken()
		if pageToken == "" {
			return indexed, nil
		}
	}
}

func (ix *Indexer) authContext(ctx context.Context) (context.Context, error) {
	token, err := ix.tokens.Sign(auth.CollectionInternalAudience)
	if err != nil {
		return nil, fmt.Errorf("indexer: sign service token: %w", err)
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
	// Other languages are skipped; their text stays searchable via fields_text.
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
