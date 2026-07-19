package search

import (
	"reflect"
	"testing"
	"time"

	"github.com/mindmesh/mindmesh/pkg/searchdsl"
)

var testTenant = Tenant{UserID: "u1", ProfileID: "p1"}

// fixedNow is a stable reference time for is:due / added:N range assertions.
var fixedNow = time.Date(2026, 6, 24, 12, 0, 0, 0, time.UTC)

func build(t *testing.T, q string) map[string]any {
	t.Helper()
	node, err := searchdsl.Parse(q)
	if err != nil {
		t.Fatalf("Parse(%q): %v", q, err)
	}
	return BuildQuery(node, testTenant, Opts{Now: fixedNow})
}

// filters extracts the tenant-filter + compiled clause list from a built query.
func filters(t *testing.T, m map[string]any) []any {
	t.Helper()
	b, ok := m["bool"].(map[string]any)
	if !ok {
		t.Fatalf("top-level query is not a bool: %#v", m)
	}
	f, ok := b["filter"].([]any)
	if !ok {
		t.Fatalf("bool.filter is not a slice: %#v", b)
	}
	return f
}

func TestBuildQuery_TenantHardFilter(t *testing.T) {
	got := filters(t, build(t, "hola"))
	// First two filter clauses must always be the tenant terms.
	want0 := map[string]any{"term": map[string]any{"user_id": "u1"}}
	want1 := map[string]any{"term": map[string]any{"profile_id": "p1"}}
	if !reflect.DeepEqual(got[0], want0) {
		t.Errorf("filter[0] = %#v, want %#v", got[0], want0)
	}
	if !reflect.DeepEqual(got[1], want1) {
		t.Errorf("filter[1] = %#v, want %#v", got[1], want1)
	}
}

func clause(t *testing.T, q string) map[string]any {
	t.Helper()
	f := filters(t, build(t, q))
	if len(f) != 3 {
		t.Fatalf("want 3 filter clauses (2 tenant + 1 compiled), got %d: %#v", len(f), f)
	}
	c, ok := f[2].(map[string]any)
	if !ok {
		t.Fatalf("compiled clause is not a map: %#v", f[2])
	}
	return c
}

func TestBuildQuery_BareTerm(t *testing.T) {
	got := clause(t, "hola")
	// A bare term is a most_fields multi_match across the agnostic + per-language
	// fields so stemmed/CJK analysis applies.
	want := map[string]any{"multi_match": map[string]any{
		"query":  "hola",
		"type":   "most_fields",
		"fields": []any{"fields_text", "fields_text_en", "fields_text_ru", "fields_text_zh"},
	}}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("bare term = %#v, want %#v", got, want)
	}
}

func TestBuildQuery_Phrase(t *testing.T) {
	got := clause(t, `"two words"`)
	want := map[string]any{"multi_match": map[string]any{
		"query":  "two words",
		"type":   "phrase",
		"fields": []any{"fields_text", "fields_text_en", "fields_text_ru", "fields_text_zh"},
	}}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("phrase = %#v, want %#v", got, want)
	}
}

func TestBuildQuery_UnknownFieldFallback(t *testing.T) {
	// An unrecognized field name falls back to a full-text multi_match on its value.
	got := clause(t, "zzz:hola")
	want := map[string]any{"multi_match": map[string]any{
		"query":  "hola",
		"type":   "most_fields",
		"fields": []any{"fields_text", "fields_text_en", "fields_text_ru", "fields_text_zh"},
	}}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("unknown field = %#v, want %#v", got, want)
	}
}

func TestBuildQuery_Wildcard(t *testing.T) {
	got := clause(t, "hol*")
	want := map[string]any{"wildcard": map[string]any{"fields_text.keyword": "hol*"}}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("wildcard = %#v, want %#v", got, want)
	}
}

func TestBuildQuery_DeckPrefix(t *testing.T) {
	// deck:Spanish matches the deck itself and any descendant (Spanish::*).
	got := clause(t, "deck:Spanish")
	want := map[string]any{"bool": map[string]any{
		"should": []any{
			map[string]any{"term": map[string]any{"deck_path": "Spanish"}},
			map[string]any{"prefix": map[string]any{"deck_path": "Spanish::"}},
		},
		"minimum_should_match": 1,
	}}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("deck = %#v, want %#v", got, want)
	}
}

func TestBuildQuery_Tag(t *testing.T) {
	got := clause(t, "tag:grammar")
	want := map[string]any{"term": map[string]any{"tags": "grammar"}}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("tag = %#v, want %#v", got, want)
	}
}

func TestBuildQuery_IsTokens(t *testing.T) {
	tests := []struct {
		q    string
		want map[string]any
	}{
		{"is:suspended", map[string]any{"term": map[string]any{"suspended": true}}},
		{"is:new", map[string]any{"term": map[string]any{"state": "new"}}},
		{"is:due", map[string]any{"range": map[string]any{"due": map[string]any{"lte": fixedNow.Format(time.RFC3339)}}}},
	}
	for _, tt := range tests {
		got := clause(t, tt.q)
		if !reflect.DeepEqual(got, tt.want) {
			t.Errorf("%s = %#v, want %#v", tt.q, got, tt.want)
		}
	}
}

func TestBuildQuery_Flag(t *testing.T) {
	got := clause(t, "flag:2")
	want := map[string]any{"term": map[string]any{"flags": 2}}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("flag = %#v, want %#v", got, want)
	}
}

func TestBuildQuery_PropRange(t *testing.T) {
	got := clause(t, "prop:ivl>=21")
	want := map[string]any{"range": map[string]any{"ivl": map[string]any{"gte": 21}}}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("prop = %#v, want %#v", got, want)
	}
}

func TestBuildQuery_Negation(t *testing.T) {
	got := clause(t, "-tag:grammar")
	want := map[string]any{"bool": map[string]any{
		"must_not": []any{
			map[string]any{"term": map[string]any{"tags": "grammar"}},
		},
	}}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("negation = %#v, want %#v", got, want)
	}
}

func TestBuildQuery_ImplicitAnd(t *testing.T) {
	got := clause(t, "tag:a tag:b")
	want := map[string]any{"bool": map[string]any{
		"must": []any{
			map[string]any{"term": map[string]any{"tags": "a"}},
			map[string]any{"term": map[string]any{"tags": "b"}},
		},
	}}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("and = %#v, want %#v", got, want)
	}
}

func TestBuildQuery_Or(t *testing.T) {
	got := clause(t, "tag:a OR tag:b")
	want := map[string]any{"bool": map[string]any{
		"should": []any{
			map[string]any{"term": map[string]any{"tags": "a"}},
			map[string]any{"term": map[string]any{"tags": "b"}},
		},
		"minimum_should_match": 1,
	}}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("or = %#v, want %#v", got, want)
	}
}
