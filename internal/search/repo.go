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

// IndexName is the concrete OpenSearch index the cards-v1 template materializes.
// Versioning the index in its name (cards-v1) lets a future mapping change roll
// forward to cards-v2 behind an alias without a destructive reindex of v1.
const IndexName = "cards-v1"

// templateName is the index-template name (distinct from the index it matches).
const templateName = "cards"

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
	FieldsText  string     `json:"fields_text"`
	AddedAt     time.Time  `json:"added_at"`
	Version     int64      `json:"version"`
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
	return &Repo{client: client, index: IndexName}, nil
}

// Ping reports whether OpenSearch is reachable; used by the service readiness probe.
func (r *Repo) Ping(ctx context.Context) error {
	if _, err := r.client.Ping(ctx, nil); err != nil {
		return fmt.Errorf("search: opensearch ping: %w", err)
	}
	return nil
}

// templateBody is the cards-v1 index template. Routing is required so every
// document colocates on its user's shard and no cross-tenant scatter is possible;
// fields_text is analyzed for full-text plus a keyword subfield for exact/prefix
// matching. Per-language analyzers are deferred (MVP uses the standard analyzer).
func templateBody() map[string]any {
	return map[string]any{
		"index_patterns": []string{IndexName},
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
						"type": "text",
						"fields": map[string]any{
							"keyword": map[string]any{"type": "keyword", "ignore_above": 1024},
						},
					},
					"added_at": map[string]any{"type": "date"},
					"version":  map[string]any{"type": "long"},
				},
			},
		},
	}
}

// EnsureTemplate creates (or replaces) the cards-v1 index template. It is
// idempotent — PUT _index_template is an upsert — so it is safe to call on
// every boot.
func (r *Repo) EnsureTemplate(ctx context.Context) error {
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

// Upsert indexes doc under its card_id, routed by user_id, using external_gte
// versioning so a redelivered or out-of-order event with a stale version is
// ignored rather than overwriting fresher data. A version conflict (HTTP 409) is
// therefore a success from the indexer's perspective and is swallowed.
func (r *Repo) Upsert(ctx context.Context, doc Document) error {
	body, err := json.Marshal(doc)
	if err != nil {
		return fmt.Errorf("search: marshal doc %s: %w", doc.CardID, err)
	}
	version := int(doc.Version)
	_, err = r.client.Index(ctx, opensearchapi.IndexReq{
		Index:      r.index,
		DocumentID: doc.CardID,
		Body:       bytes.NewReader(body),
		Params: opensearchapi.IndexParams{
			Routing:     doc.UserID,
			Version:     &version,
			VersionType: "external_gte",
		},
	})
	if err != nil {
		if isVersionConflict(err) {
			return nil // a newer version already won; nothing to do
		}
		return fmt.Errorf("search: index doc %s: %w", doc.CardID, err)
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

// isVersionConflict reports whether err is an OpenSearch 409 version conflict.
func isVersionConflict(err error) bool { return statusCode(err) == http.StatusConflict }

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
