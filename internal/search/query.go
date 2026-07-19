// Package search translates the parsed query DSL (pkg/searchdsl) into OpenSearch
// queries and indexes/serves the card documents the Search service owns.
package search

import (
	"time"

	"github.com/mindmesh/mindmesh/pkg/searchdsl"
)

// Tenant is the trusted owner scope a query is hard-filtered to. It always comes
// from the gateway-injected JWT context, never from client input.
type Tenant struct {
	UserID    string
	ProfileID string
}

// Opts carries query-time context the DSL can't express on its own.
type Opts struct {
	// Now is the reference time for relative tokens (is:due, added:N). Zero means
	// time.Now() at build time.
	Now time.Time
}

// BuildQuery compiles a parsed DSL node into an OpenSearch query body's "query"
// object. The result is a bool whose filter clauses are, in order, the two
// tenant terms (user_id, profile_id) followed by the compiled DSL clause. Using
// filter context means no scoring and cacheable results, which suits browsing.
func BuildQuery(node searchdsl.Node, t Tenant, opts Opts) map[string]any {
	if opts.Now.IsZero() {
		opts.Now = time.Now()
	}
	// The tenant terms are always present and always first. A nil node means an
	// unconstrained listing (empty query string), so we add no DSL clause and the
	// bool matches every document the tenant owns.
	filter := []any{
		term("user_id", t.UserID),
		term("profile_id", t.ProfileID),
	}
	if node != nil {
		filter = append(filter, compile(node, opts))
	}
	return map[string]any{
		"bool": map[string]any{"filter": filter},
	}
}

// compile translates a single DSL node into an OpenSearch clause.
func compile(node searchdsl.Node, opts Opts) map[string]any {
	switch n := node.(type) {
	case searchdsl.And:
		return boolClause("must", n.Children, opts)
	case searchdsl.Or:
		c := boolClause("should", n.Children, opts)
		c["bool"].(map[string]any)["minimum_should_match"] = 1
		return c
	case searchdsl.Not:
		return map[string]any{"bool": map[string]any{
			"must_not": []any{compile(n.Child, opts)},
		}}
	case searchdsl.Prop:
		return propClause(n)
	case searchdsl.Term:
		return termClause(n, opts)
	default:
		// Unknown node: match nothing rather than everything.
		return map[string]any{"match_none": map[string]any{}}
	}
}

func boolClause(occur string, children []searchdsl.Node, opts Opts) map[string]any {
	clauses := make([]any, 0, len(children))
	for _, c := range children {
		clauses = append(clauses, compile(c, opts))
	}
	return map[string]any{"bool": map[string]any{occur: clauses}}
}

// textFields are the full-text fields a bare/phrase term matches against: the
// language-agnostic fields_text (standard analyzer) plus the per-language fields,
// each of which analyzes the query with its own analyzer (english/russian stemming,
// cjk bigram). A doc matches if any field matches, so a stemmed or CJK query hits
// text the standard analyzer alone would miss.
var textFields = []any{"fields_text", "fields_text_en", "fields_text_ru", "fields_text_zh"}

// termClause maps a Term leaf to a clause based on its field. A bare term (no
// field) is full-text across the language fields; a wildcard goes against the
// untokenized keyword subfield; a phrase uses a phrase multi_match.
func termClause(tm searchdsl.Term, opts Opts) map[string]any {
	switch tm.Field {
	case "":
		switch {
		case tm.Wildcard:
			return map[string]any{"wildcard": map[string]any{"fields_text.keyword": tm.Value + "*"}}
		case tm.Phrase:
			return multiMatch(tm.Value, "phrase")
		default:
			return multiMatch(tm.Value, "most_fields")
		}
	case "deck":
		// deck:X matches the deck itself and every descendant (X::*).
		return map[string]any{"bool": map[string]any{
			"should": []any{
				term("deck_path", tm.Value),
				map[string]any{"prefix": map[string]any{"deck_path": tm.Value + "::"}},
			},
			"minimum_should_match": 1,
		}}
	case "tag":
		if tm.Wildcard {
			return map[string]any{"wildcard": map[string]any{"tags": tm.Value + "*"}}
		}
		return term("tags", tm.Value)
	case "is":
		return isClause(tm.Value, opts)
	case "flag":
		return term("flags", atoiOr0(tm.Value))
	case "added":
		// added:N → created within the last N days.
		since := opts.Now.AddDate(0, 0, -atoiOr0(tm.Value))
		return rangeClause("added_at", "gte", since.Format(time.RFC3339))
	default:
		// Unknown field name: full-text fallback across the language fields.
		return multiMatch(tm.Value, "most_fields")
	}
}

// multiMatch builds a multi_match over the full-text fields with the given type
// ("most_fields" for bare terms, "phrase" for quoted phrases).
func multiMatch(value, matchType string) map[string]any {
	return map[string]any{"multi_match": map[string]any{
		"query":  value,
		"type":   matchType,
		"fields": textFields,
	}}
}

// isClause maps is:<state> tokens to the corresponding card filter.
func isClause(value string, opts Opts) map[string]any {
	switch value {
	case "suspended":
		return term("suspended", true)
	case "new":
		return term("state", "new")
	case "due":
		return rangeClause("due", "lte", opts.Now.Format(time.RFC3339))
	default:
		return term("state", value)
	}
}

// propClause maps a numeric prop comparison to an OpenSearch range query.
func propClause(p searchdsl.Prop) map[string]any {
	if p.Op == "=" {
		return term(p.Name, p.Value)
	}
	return rangeClause(p.Name, rangeOp(p.Op), p.Value)
}

// rangeOp converts a DSL comparison operator to its OpenSearch range key.
func rangeOp(op string) string {
	switch op {
	case ">=":
		return "gte"
	case "<=":
		return "lte"
	case ">":
		return "gt"
	case "<":
		return "lt"
	default:
		return "gte"
	}
}

func term(field string, value any) map[string]any {
	return map[string]any{"term": map[string]any{field: value}}
}

func rangeClause(field, op string, value any) map[string]any {
	return map[string]any{"range": map[string]any{field: map[string]any{op: value}}}
}

// atoiOr0 parses a decimal string, returning 0 on any error. The DSL parser
// already validates prop numbers; flag:/added: values are best-effort.
func atoiOr0(s string) int {
	n := 0
	neg := false
	for i, r := range s {
		if i == 0 && r == '-' {
			neg = true
			continue
		}
		if r < '0' || r > '9' {
			return 0
		}
		n = n*10 + int(r-'0')
	}
	if neg {
		return -n
	}
	return n
}
