package search

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/opensearch-project/opensearch-go/v4"
	"github.com/opensearch-project/opensearch-go/v4/opensearchapi"
)

const (
	// AliasName is the read/write alias every card operation targets. It resolves
	// to exactly one concrete index at a time (currentIndex); a mapping/analyzer
	// change rolls a new concrete index in behind the alias without a destructive
	// in-place reindex — see EnsureIndex.
	AliasName = "cards"

	// currentIndex is the concrete index this build's mapping materializes; the
	// alias is pointed here on boot. It is versioned in the name (cards-v2 adds the
	// per-language analyzer fields over cards-v1's standard-only mapping) so the next
	// mapping change bumps to cards-v3 and reindexes behind the alias the same way.
	currentIndex = "cards-v2"

	// legacyIndex is the pre-alias v1 index that early deployments wrote to directly.
	// When it exists and the alias does not, EnsureIndex reindexes it into
	// currentIndex (behavior-parity copy) before pointing the alias at currentIndex.
	legacyIndex = "cards-v1"

	// templateName is the index-template name; its index_patterns match every
	// cards-v* concrete index so each rolls up with the same base mapping.
	templateName = "cards"
)

// Document is the denormalized card projection stored in OpenSearch. Its JSON
// field names are the contract the query builder (query.go) filters against, so
// the two must stay in lockstep. It mirrors collection.SearchDoc but is the
// Search service's own type, decoded from the GetSearchDocs response.
type Document struct {
	CardID      string     `json:"card_id"`
	UserID      string     `json:"user_id"`
	ProfileID   string     `json:"profile_id"`
	NoteID      string     `json:"note_id"`
	DeckID      string     `json:"deck_id"`
	DeckPath    string     `json:"deck_path"`
	TemplateOrd int32      `json:"template_ord"`
	State       string     `json:"state"` // "new"|"learning"|"review"|"relearning"
	Suspended   bool       `json:"suspended"`
	Due         *time.Time `json:"due,omitempty"`
	Ivl         int32      `json:"ivl"`
	Reps        int32      `json:"reps"`
	Lapses      int32      `json:"lapses"`
	Flags       int32      `json:"flags"`
	Tags        []string   `json:"tags"`
	FieldsText  string     `json:"fields_text"` // language-agnostic, standard analyzer
	// FieldsTextEn/Ru/Zh hold the note's field text grouped by language tag, each
	// indexed under its own analyzer (english/russian/cjk) so stemmed and CJK
	// queries match. Empty when the note has no field tagged that language.
	FieldsTextEn string    `json:"fields_text_en,omitempty"`
	FieldsTextRu string    `json:"fields_text_ru,omitempty"`
	FieldsTextZh string    `json:"fields_text_zh,omitempty"`
	AddedAt      time.Time `json:"added_at"`
	Version      int64     `json:"version"`
}

// RepoConfig configures the OpenSearch connection.
type RepoConfig struct {
	Addresses []string // OPENSEARCH_ADDRESSES comma-split, default ["http://localhost:9200"]
	Username  string   // OPENSEARCH_USERNAME (optional; dev runs with security disabled)
	Password  string   // OPENSEARCH_PASSWORD
}

// LoadRepoConfig reads OpenSearch configuration from the environment.
func LoadRepoConfig() RepoConfig {
	addrs := []string{"http://localhost:9200"}
	if v := os.Getenv("OPENSEARCH_ADDRESSES"); v != "" {
		var filtered []string
		for _, p := range strings.Split(v, ",") {
			if p = strings.TrimSpace(p); p != "" {
				filtered = append(filtered, p)
			}
		}
		if len(filtered) > 0 {
			addrs = filtered
		}
	}
	return RepoConfig{
		Addresses: addrs,
		Username:  os.Getenv("OPENSEARCH_USERNAME"),
		Password:  os.Getenv("OPENSEARCH_PASSWORD"),
	}
}

// Repo wraps the OpenSearch client with the card-document operations the Search
// service needs: template bootstrap, idempotent upsert, delete, and query.
type Repo struct {
	client *opensearchapi.Client
	index  string
}

// NewRepo connects to OpenSearch using cfg.
func NewRepo(cfg RepoConfig) (*Repo, error) {
	client, err := opensearchapi.NewClient(opensearchapi.Config{
		Client: opensearch.Config{
			Addresses: cfg.Addresses,
			Username:  cfg.Username,
			Password:  cfg.Password,
		},
	})
	if err != nil {
		return nil, fmt.Errorf("search: new opensearch client: %w", err)
	}
	return &Repo{client: client, index: AliasName}, nil
}

// Ping reports whether OpenSearch is reachable; used by the service readiness probe.
func (r *Repo) Ping(ctx context.Context) error {
	if _, err := r.client.Ping(ctx, nil); err != nil {
		return fmt.Errorf("search: opensearch ping: %w", err)
	}
	return nil
}

// templateBody is the shared cards-v* index template. Routing is required so every
// document colocates on its user's shard and no cross-tenant scatter is possible.
// fields_text is the language-agnostic full-text field (standard analyzer) plus a
// keyword subfield for exact/prefix matching; fields_text_{en,ru,zh} index the note's
// per-language field text under the matching built-in analyzer (english/russian
// stemming, cjk bigram segmentation) so stemmed and CJK queries match. The analyzers
// are all built-in (analysis-common) — no plugin/image change. Upgrading zh to
// dictionary segmentation (smartcn/ICU) needs a plugin and is deferred.
func templateBody() map[string]any {
	return map[string]any{
		"index_patterns": []string{"cards-v*"},
		"template": map[string]any{
			"settings": map[string]any{
				"number_of_shards":   1,
				"number_of_replicas": 0,
			},
			"mappings": map[string]any{
				"_routing": map[string]any{"required": true},
				"properties": map[string]any{
					"card_id":      map[string]any{"type": "keyword"},
					"user_id":      map[string]any{"type": "keyword"},
					"profile_id":   map[string]any{"type": "keyword"},
					"note_id":      map[string]any{"type": "keyword"},
					"deck_id":      map[string]any{"type": "keyword"},
					"deck_path":    map[string]any{"type": "keyword"},
					"template_ord": map[string]any{"type": "integer"},
					"state":        map[string]any{"type": "keyword"},
					"suspended":    map[string]any{"type": "boolean"},
					"due":          map[string]any{"type": "date"},
					"ivl":          map[string]any{"type": "integer"},
					"reps":         map[string]any{"type": "integer"},
					"lapses":       map[string]any{"type": "integer"},
					"flags":        map[string]any{"type": "integer"},
					"tags":         map[string]any{"type": "keyword"},
					"fields_text": map[string]any{
						"type":     "text",
						"analyzer": "standard",
						"fields": map[string]any{
							"keyword": map[string]any{"type": "keyword", "ignore_above": 1024},
						},
					},
					"fields_text_en": map[string]any{"type": "text", "analyzer": "english"},
					"fields_text_ru": map[string]any{"type": "text", "analyzer": "russian"},
					"fields_text_zh": map[string]any{"type": "text", "analyzer": "cjk"},
					"added_at":       map[string]any{"type": "date"},
					"version":        map[string]any{"type": "long"},
				},
			},
		},
	}
}

// EnsureIndex idempotently bootstraps the search index on boot: it upserts the
// cards-v* template, ensures the current concrete index exists, and points the
// read/write alias at it. On a deployment that predates the alias (a bare
// cards-v1 index, no alias), it reindexes cards-v1 into the current index for
// behavior parity before swapping the alias in a single atomic action, so search
// never regresses across the cutover. Safe to call on every boot.
func (r *Repo) EnsureIndex(ctx context.Context) error {
	if err := r.ensureTemplate(ctx); err != nil {
		return err
	}
	// If the alias already resolves, a previous boot completed the bootstrap.
	aliasExists, err := r.resourceExists(ctx, AliasName)
	if err != nil {
		return err
	}
	if aliasExists {
		return nil
	}
	currentExists, err := r.resourceExists(ctx, currentIndex)
	if err != nil {
		return err
	}
	if !currentExists {
		if err := r.createIndex(ctx, currentIndex); err != nil {
			return err
		}
		// Only reindex into a freshly created current index, and only from a
		// pre-alias legacy index — this copies the language-agnostic fields for
		// parity; per-language buckets fill in via the forward path / backfill.
		legacyExists, err := r.resourceExists(ctx, legacyIndex)
		if err != nil {
			return err
		}
		if legacyExists {
			if err := r.Reindex(ctx, legacyIndex, currentIndex); err != nil {
				return err
			}
		}
	}
	return r.pointAlias(ctx, AliasName, currentIndex)
}

// ensureTemplate upserts the cards-v* index template (PUT _index_template is
// idempotent).
func (r *Repo) ensureTemplate(ctx context.Context) error {
	body, err := json.Marshal(templateBody())
	if err != nil {
		return fmt.Errorf("search: marshal template: %w", err)
	}
	_, err = r.client.IndexTemplate.Create(ctx, opensearchapi.IndexTemplateCreateReq{
		IndexTemplate: templateName,
		Body:          bytes.NewReader(body),
	})
	if err != nil {
		return fmt.Errorf("search: put index template: %w", err)
	}
	return nil
}

// resourceExists reports whether an index or alias with the given name exists. A
// HEAD /{name} returns 200 for both indices and aliases; a 404 is reported as a
// non-error false so callers can branch on it.
func (r *Repo) resourceExists(ctx context.Context, name string) (bool, error) {
	resp, err := r.client.Indices.Exists(ctx, opensearchapi.IndicesExistsReq{Indices: []string{name}})
	if err == nil {
		return true, nil
	}
	if resp != nil && resp.StatusCode == http.StatusNotFound {
		return false, nil
	}
	return false, fmt.Errorf("search: exists %s: %w", name, err)
}

// createIndex creates a concrete index; the cards-v* template supplies the
// mapping and settings.
func (r *Repo) createIndex(ctx context.Context, name string) error {
	_, err := r.client.Indices.Create(ctx, opensearchapi.IndicesCreateReq{Index: name})
	if err != nil {
		return fmt.Errorf("search: create index %s: %w", name, err)
	}
	return nil
}

// pointAlias makes alias resolve to index, adding it in a single _aliases action.
func (r *Repo) pointAlias(ctx context.Context, alias, index string) error {
	body, err := json.Marshal(map[string]any{
		"actions": []any{
			map[string]any{"add": map[string]any{"index": index, "alias": alias}},
		},
	})
	if err != nil {
		return fmt.Errorf("search: marshal alias action: %w", err)
	}
	if _, err := r.client.Aliases(ctx, opensearchapi.AliasesReq{Body: bytes.NewReader(body)}); err != nil {
		return fmt.Errorf("search: point alias %s->%s: %w", alias, index, err)
	}
	return nil
}

// Reindex synchronously copies every document from src into dst, carrying each
// doc's external version so external_gte upserts stay monotonic afterward. Used at
// the alias cutover to give the new index behavior parity with the old one.
func (r *Repo) Reindex(ctx context.Context, src, dst string) error {
	body, err := json.Marshal(map[string]any{
		"source": map[string]any{"index": src},
		"dest":   map[string]any{"index": dst, "version_type": "external"},
	})
	if err != nil {
		return fmt.Errorf("search: marshal reindex: %w", err)
	}
	if _, err := r.client.Reindex(ctx, opensearchapi.ReindexReq{Body: bytes.NewReader(body)}); err != nil {
		return fmt.Errorf("search: reindex %s->%s: %w", src, dst, err)
	}
	return nil
}

// Upsert bulk-indexes docs under their card_id, routed by user_id, with
// external_gte versioning: a stale version (per-item 409) is skipped, not an error.
func (r *Repo) Upsert(ctx context.Context, docs ...Document) error {
	if len(docs) == 0 {
		return nil
	}
	var body bytes.Buffer
	enc := json.NewEncoder(&body)
	for i := range docs {
		action := map[string]any{"index": map[string]any{
			"_id":          docs[i].CardID,
			"routing":      docs[i].UserID,
			"version":      docs[i].Version,
			"version_type": "external_gte",
		}}
		if err := enc.Encode(action); err != nil {
			return fmt.Errorf("search: marshal bulk action %s: %w", docs[i].CardID, err)
		}
		if err := enc.Encode(docs[i]); err != nil {
			return fmt.Errorf("search: marshal doc %s: %w", docs[i].CardID, err)
		}
	}
	resp, err := r.client.Bulk(ctx, opensearchapi.BulkReq{Index: r.index, Body: &body})
	if err != nil {
		return fmt.Errorf("search: bulk index: %w", err)
	}
	if !resp.Errors {
		return nil
	}
	for _, item := range resp.Items {
		for _, res := range item {
			if res.Error != nil && res.Status != http.StatusConflict {
				return fmt.Errorf("search: index doc %s: %d %s: %s", res.ID, res.Status, res.Error.Type, res.Error.Reason)
			}
		}
	}
	return nil
}

// Delete removes a single card document by id, routed by user_id. A missing
// document (404) is not an error: the indexer is at-least-once, so a delete may
// be redelivered after the doc is already gone.
func (r *Repo) Delete(ctx context.Context, userID, cardID string) error {
	_, err := r.client.Document.Delete(ctx, opensearchapi.DocumentDeleteReq{
		Index:      r.index,
		DocumentID: cardID,
		Params:     opensearchapi.DocumentDeleteParams{Routing: userID},
	})
	if err != nil {
		if isNotFound(err) {
			return nil
		}
		return fmt.Errorf("search: delete doc %s: %w", cardID, err)
	}
	return nil
}

// DeleteByField removes every document of a user matching field==value, routed
// by user_id. Used to drop all of a note's or deck's cards on a *.deleted event
// without the indexer having to enumerate the card ids itself.
func (r *Repo) DeleteByField(ctx context.Context, userID, field, value string) error {
	q := map[string]any{
		"query": map[string]any{
			"bool": map[string]any{
				"filter": []any{
					map[string]any{"term": map[string]any{"user_id": userID}},
					map[string]any{"term": map[string]any{field: value}},
				},
			},
		},
	}
	body, err := json.Marshal(q)
	if err != nil {
		return fmt.Errorf("search: marshal delete-by-query: %w", err)
	}
	_, err = r.client.Document.DeleteByQuery(ctx, opensearchapi.DocumentDeleteByQueryReq{
		Indices: []string{r.index},
		Body:    bytes.NewReader(body),
		Params:  opensearchapi.DocumentDeleteByQueryParams{Routing: []string{userID}},
	})
	if err != nil {
		if isNotFound(err) {
			return nil
		}
		return fmt.Errorf("search: delete by %s=%s: %w", field, value, err)
	}
	return nil
}

// Refresh forces the index to make recent writes visible to search. Production
// relies on OpenSearch's periodic refresh (sub-second), so this is used mainly
// by tests that must read their own writes immediately.
func (r *Repo) Refresh(ctx context.Context) error {
	_, err := r.client.Indices.Refresh(ctx, &opensearchapi.IndicesRefreshReq{
		Indices: []string{r.index},
	})
	if err != nil {
		return fmt.Errorf("search: refresh index: %w", err)
	}
	return nil
}

// SearchResult is one page of a query.
type SearchResult struct {
	Docs       []Document
	NextCursor string // opaque keyset cursor; empty when the page is the last
	Total      int    // total matching documents (lower bound under high cardinality)
}

// Search runs query (the compiled bool body) against the index for one user,
// routed to that user's shard, sorted by added_at desc then card_id for a total
// keyset order. searchAfter resumes after a previous page's last sort values.
func (r *Repo) Search(ctx context.Context, userID string, query map[string]any, searchAfter []any, limit int) (SearchResult, error) {
	body := map[string]any{
		"size":  limit,
		"query": query,
		"sort": []any{
			map[string]any{"added_at": map[string]any{"order": "desc"}},
			map[string]any{"card_id": map[string]any{"order": "asc"}},
		},
		"track_total_hits": true,
	}
	if len(searchAfter) > 0 {
		body["search_after"] = searchAfter
	}
	raw, err := json.Marshal(body)
	if err != nil {
		return SearchResult{}, fmt.Errorf("search: marshal query: %w", err)
	}

	resp, err := r.client.Search(ctx, &opensearchapi.SearchReq{
		Indices: []string{r.index},
		Body:    bytes.NewReader(raw),
		Params:  opensearchapi.SearchParams{Routing: []string{userID}},
	})
	if err != nil {
		return SearchResult{}, fmt.Errorf("search: query: %w", err)
	}

	out := SearchResult{Total: resp.Hits.Total.Value}
	for _, hit := range resp.Hits.Hits {
		var doc Document
		if err := json.Unmarshal(hit.Source, &doc); err != nil {
			return SearchResult{}, fmt.Errorf("search: decode hit %s: %w", hit.ID, err)
		}
		out.Docs = append(out.Docs, doc)
	}
	// A full page implies there may be more; encode the last hit's sort values as
	// the next cursor. A short page is terminal.
	if len(resp.Hits.Hits) == limit && limit > 0 {
		last := resp.Hits.Hits[len(resp.Hits.Hits)-1]
		cursor, err := encodeCursor(last.Sort)
		if err != nil {
			return SearchResult{}, err
		}
		out.NextCursor = cursor
	}
	return out, nil
}

// isNotFound reports whether err is an OpenSearch 404.
func isNotFound(err error) bool { return statusCode(err) == http.StatusNotFound }

// statusCode extracts the HTTP status from an opensearch error. The client
// returns either a StructError (structured error body) or a StringError (opaque
// body); both expose a numeric Status. Returns 0 when err is neither.
func statusCode(err error) int {
	var se *opensearch.StructError
	if errors.As(err, &se) {
		return se.Status
	}
	var ste *opensearch.StringError
	if errors.As(err, &ste) {
		return ste.Status
	}
	return 0
}
