package template

import (
	"errors"
	"testing"
)

func TestRender(t *testing.T) {
	tests := []struct {
		name    string
		tmpl    string
		fields  map[string]string
		opts    Opts
		want    string
		wantErr bool
	}{
		{
			name:   "plain field substitution",
			tmpl:   "Front: {{Word}}",
			fields: map[string]string{"Word": "casa"},
			want:   "Front: casa",
		},
		{
			name:   "unknown field renders empty",
			tmpl:   "x{{Nope}}y",
			fields: map[string]string{"Word": "casa"},
			want:   "xy",
		},
		{
			name:   "multiple fields",
			tmpl:   "{{A}}-{{B}}",
			fields: map[string]string{"A": "1", "B": "2"},
			want:   "1-2",
		},
		{
			name:   "field with spaces in name",
			tmpl:   "{{ Word }}",
			fields: map[string]string{"Word": "casa"},
			want:   "casa",
		},
		{
			name:   "section shown when field non-empty",
			tmpl:   "{{#Extra}}({{Extra}}){{/Extra}}",
			fields: map[string]string{"Extra": "note"},
			want:   "(note)",
		},
		{
			name:   "section hidden when field empty string",
			tmpl:   "a{{#Extra}}({{Extra}}){{/Extra}}b",
			fields: map[string]string{"Extra": ""},
			want:   "ab",
		},
		{
			name:   "section hidden when field absent",
			tmpl:   "a{{#Extra}}x{{/Extra}}b",
			fields: map[string]string{},
			want:   "ab",
		},
		{
			name:   "section shown when whitespace-only (non-empty per Anki)",
			tmpl:   "{{#Extra}}HIT{{/Extra}}",
			fields: map[string]string{"Extra": "   "},
			want:   "HIT",
		},
		{
			name:   "inverted section hidden when non-empty",
			tmpl:   "{{^Extra}}none{{/Extra}}",
			fields: map[string]string{"Extra": "note"},
			want:   "",
		},
		{
			name:   "inverted section shown when empty",
			tmpl:   "{{^Extra}}none{{/Extra}}",
			fields: map[string]string{"Extra": ""},
			want:   "none",
		},
		{
			name:   "inverted section shown when absent",
			tmpl:   "{{^Extra}}none{{/Extra}}",
			fields: map[string]string{},
			want:   "none",
		},
		{
			name:   "nested sections both true",
			tmpl:   "{{#A}}{{#B}}x{{/B}}{{/A}}",
			fields: map[string]string{"A": "1", "B": "1"},
			want:   "x",
		},
		{
			name:   "nested sections outer true inner false",
			tmpl:   "{{#A}}[{{#B}}x{{/B}}]{{/A}}",
			fields: map[string]string{"A": "1"},
			want:   "[]",
		},
		{
			name:   "nested sections outer false suppresses inner",
			tmpl:   "{{#A}}{{#B}}x{{/B}}{{/A}}",
			fields: map[string]string{"B": "1"},
			want:   "",
		},
		{
			name:   "nested mixed normal and inverted",
			tmpl:   "{{#A}}{{^B}}only-a{{/B}}{{/A}}",
			fields: map[string]string{"A": "1"},
			want:   "only-a",
		},
		{
			name:   "frontside substituted on back",
			tmpl:   "{{FrontSide}}<hr>{{Back}}",
			fields: map[string]string{"Back": "answer"},
			opts:   Opts{FrontSide: "rendered-front", Back: true},
			want:   "rendered-front<hr>answer",
		},
		{
			name:   "frontside empty when not provided",
			tmpl:   "{{FrontSide}}<hr>{{Back}}",
			fields: map[string]string{"Back": "answer"},
			want:   "<hr>answer",
		},
		{
			name:   "html in field passed through verbatim (no escaping)",
			tmpl:   "{{Word}}",
			fields: map[string]string{"Word": "<b>casa</b> & <i>x</i>"},
			want:   "<b>casa</b> & <i>x</i>",
		},
		{
			name:   "script in field passed through verbatim (no execution/sanitization)",
			tmpl:   "{{Word}}",
			fields: map[string]string{"Word": "<script>alert(1)</script>"},
			want:   "<script>alert(1)</script>",
		},
		{
			name:   "no tags returns template as-is",
			tmpl:   "just plain text",
			fields: map[string]string{},
			want:   "just plain text",
		},
		{
			name:    "unclosed section errors",
			tmpl:    "{{#A}}x",
			fields:  map[string]string{"A": "1"},
			wantErr: true,
		},
		{
			name:    "mismatched close tag errors",
			tmpl:    "{{#A}}x{{/B}}",
			fields:  map[string]string{"A": "1"},
			wantErr: true,
		},
		{
			name:    "close without open errors",
			tmpl:    "x{{/A}}",
			fields:  map[string]string{},
			wantErr: true,
		},
		{
			name:    "empty tag errors",
			tmpl:    "a{{}}b",
			fields:  map[string]string{"": "leak"},
			wantErr: true,
		},
		{
			name:    "whitespace-only tag errors",
			tmpl:    "a{{   }}b",
			fields:  map[string]string{"": "leak"},
			wantErr: true,
		},
		{
			name:    "empty section name errors",
			tmpl:    "{{#}}x{{/}}",
			fields:  map[string]string{},
			wantErr: true,
		},
		{
			name:    "empty cloze field name errors",
			tmpl:    "{{cloze:}}",
			fields:  map[string]string{},
			wantErr: true,
		},
		{
			name:    "unterminated tag errors",
			tmpl:    "a{{Word",
			fields:  map[string]string{"Word": "x"},
			wantErr: true,
		},
		{
			name:   "adjacent tags",
			tmpl:   "{{A}}{{B}}",
			fields: map[string]string{"A": "1", "B": "2"},
			want:   "12",
		},
		{
			name:   "field value containing tag-like text is not re-parsed",
			tmpl:   "{{Word}}",
			fields: map[string]string{"Word": "{{Other}} {{c1::x}} {{#S}}y{{/S}}", "Other": "SHOULD-NOT-APPEAR"},
			want:   "{{Other}} {{c1::x}} {{#S}}y{{/S}}",
		},
		{
			name:    "nested open delimiter inside a tag errors",
			tmpl:    "{{ {{Name}} }}",
			fields:  map[string]string{"Name": "v"},
			wantErr: true,
		},
		{
			name:   "hidden section does not render its child fields",
			tmpl:   "{{#A}}{{B}}{{/A}}",
			fields: map[string]string{"B": "should-not-appear"},
			want:   "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := Render(tt.tmpl, tt.fields, tt.opts)
			if tt.wantErr {
				if err == nil {
					t.Fatalf("Render() expected error, got nil (out=%q)", got)
				}
				return
			}
			if err != nil {
				t.Fatalf("Render() unexpected error: %v", err)
			}
			if got != tt.want {
				t.Errorf("Render() = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestRenderMalformedErrorIsTyped(t *testing.T) {
	_, err := Render("{{#A}}x", map[string]string{}, Opts{})
	if !errors.Is(err, ErrMalformedTemplate) {
		t.Fatalf("expected ErrMalformedTemplate, got %v", err)
	}
}
