package search

import "testing"

// TestTemplateBody_LanguageAnalyzers asserts the index template maps the per-language
// full-text fields to their built-in analyzers and keeps the agnostic fields_text +
// keyword subfield. This is the index-time half of language-analyzer selection.
func TestTemplateBody_LanguageAnalyzers(t *testing.T) {
	props := templateBody()["template"].(map[string]any)["mappings"].(map[string]any)["properties"].(map[string]any)

	wantAnalyzer := map[string]string{
		"fields_text_en": "english",
		"fields_text_ru": "russian",
		"fields_text_zh": "cjk",
	}
	for field, analyzer := range wantAnalyzer {
		f, ok := props[field].(map[string]any)
		if !ok {
			t.Fatalf("mapping missing %q", field)
		}
		if f["type"] != "text" {
			t.Errorf("%s type = %v, want text", field, f["type"])
		}
		if f["analyzer"] != analyzer {
			t.Errorf("%s analyzer = %v, want %s", field, f["analyzer"], analyzer)
		}
	}

	// The agnostic field keeps the standard analyzer and its keyword subfield.
	ft, ok := props["fields_text"].(map[string]any)
	if !ok {
		t.Fatal("mapping missing fields_text")
	}
	if ft["analyzer"] != "standard" {
		t.Errorf("fields_text analyzer = %v, want standard", ft["analyzer"])
	}
	if _, ok := ft["fields"].(map[string]any)["keyword"]; !ok {
		t.Error("fields_text missing keyword subfield")
	}

	// The template must match every versioned concrete index behind the alias.
	patterns := templateBody()["index_patterns"].([]string)
	if len(patterns) != 1 || patterns[0] != "cards-v*" {
		t.Errorf("index_patterns = %v, want [cards-v*]", patterns)
	}
}
