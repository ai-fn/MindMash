package template

import (
	"reflect"
	"testing"
)

func TestClozeIndices(t *testing.T) {
	tests := []struct {
		name   string
		fields map[string]string
		want   []int
	}{
		{
			name:   "none",
			fields: map[string]string{"Text": "no cloze here"},
			want:   nil,
		},
		{
			name:   "empty map",
			fields: map[string]string{},
			want:   nil,
		},
		{
			name:   "single ordinal",
			fields: map[string]string{"Text": "the {{c1::answer}} is here"},
			want:   []int{1},
		},
		{
			name:   "sorted distinct across fields, out of order",
			fields: map[string]string{"A": "{{c3::z}} and {{c1::a}}", "B": "{{c2::b}} {{c1::dup}}"},
			want:   []int{1, 2, 3},
		},
		{
			name:   "duplicate ordinal collapses",
			fields: map[string]string{"Text": "{{c1::x}} {{c1::y}} {{c1::z}}"},
			want:   []int{1},
		},
		{
			name:   "ordinal with hint counted",
			fields: map[string]string{"Text": "{{c2::ans::hint}}"},
			want:   []int{2},
		},
		{
			name:   "multi-digit ordinal",
			fields: map[string]string{"Text": "{{c12::x}} {{c2::y}}"},
			want:   []int{2, 12},
		},
		{
			name:   "ordinal zero ignored (1-based; would be unstudyable)",
			fields: map[string]string{"Text": "{{c0::x}} {{c1::y}}"},
			want:   []int{1},
		},
		{
			name:   "overflowing ordinal ignored (Atoi fails)",
			fields: map[string]string{"Text": "{{c99999999999999999999::a}} {{c1::b}}"},
			want:   []int{1},
		},
		{
			name:   "multi-line answer still counted",
			fields: map[string]string{"Text": "{{c1::line one\nline two}}"},
			want:   []int{1},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := ClozeIndices(tt.fields)
			if !reflect.DeepEqual(got, tt.want) {
				t.Errorf("ClozeIndices() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestClozeRendering(t *testing.T) {
	tests := []struct {
		name   string
		tmpl   string
		fields map[string]string
		opts   Opts
		want   string
	}{
		{
			name:   "front active ordinal masked no hint",
			tmpl:   "{{cloze:Text}}",
			fields: map[string]string{"Text": "the {{c1::answer}} here"},
			opts:   Opts{ClozeOrd: 1, Back: false},
			want:   "the [...] here",
		},
		{
			name:   "front active ordinal masked with hint",
			tmpl:   "{{cloze:Text}}",
			fields: map[string]string{"Text": "the {{c1::answer::myhint}} here"},
			opts:   Opts{ClozeOrd: 1, Back: false},
			want:   "the [myhint] here",
		},
		{
			name:   "front non-active ordinal revealed plain (hint dropped)",
			tmpl:   "{{cloze:Text}}",
			fields: map[string]string{"Text": "{{c1::one}} {{c2::two::h}}"},
			opts:   Opts{ClozeOrd: 1, Back: false},
			want:   "[...] two",
		},
		{
			name:   "back active ordinal revealed (hint dropped)",
			tmpl:   "{{cloze:Text}}",
			fields: map[string]string{"Text": "the {{c1::answer::hint}} here"},
			opts:   Opts{ClozeOrd: 1, Back: true},
			want:   "the answer here",
		},
		{
			name:   "back active ordinal revealed no hint",
			tmpl:   "{{cloze:Text}}",
			fields: map[string]string{"Text": "the {{c1::answer}} here"},
			opts:   Opts{ClozeOrd: 1, Back: true},
			want:   "the answer here",
		},
		{
			name:   "back non-active ordinal also revealed plain",
			tmpl:   "{{cloze:Text}}",
			fields: map[string]string{"Text": "{{c1::one}} {{c2::two}}"},
			opts:   Opts{ClozeOrd: 1, Back: true},
			want:   "one two",
		},
		{
			name:   "ord zero reveals all plain (front)",
			tmpl:   "{{cloze:Text}}",
			fields: map[string]string{"Text": "{{c1::one::h}} {{c2::two}}"},
			opts:   Opts{ClozeOrd: 0, Back: false},
			want:   "one two",
		},
		{
			name:   "ord zero reveals all plain (back)",
			tmpl:   "{{cloze:Text}}",
			fields: map[string]string{"Text": "{{c1::one}} {{c2::two::h}}"},
			opts:   Opts{ClozeOrd: 0, Back: true},
			want:   "one two",
		},
		{
			name:   "cloze on unknown field renders empty",
			tmpl:   "{{cloze:Nope}}",
			fields: map[string]string{"Text": "{{c1::x}}"},
			opts:   Opts{ClozeOrd: 1},
			want:   "",
		},
		{
			name:   "cloze field content with html preserved",
			tmpl:   "{{cloze:Text}}",
			fields: map[string]string{"Text": "<b>{{c1::bold}}</b>"},
			opts:   Opts{ClozeOrd: 1, Back: true},
			want:   "<b>bold</b>",
		},
		{
			name:   "cloze field with no deletions passes through",
			tmpl:   "{{cloze:Text}}",
			fields: map[string]string{"Text": "plain text"},
			opts:   Opts{ClozeOrd: 1},
			want:   "plain text",
		},
		{
			name:   "multiple active deletions same ordinal masked",
			tmpl:   "{{cloze:Text}}",
			fields: map[string]string{"Text": "{{c1::a}} and {{c1::b}}"},
			opts:   Opts{ClozeOrd: 1, Back: false},
			want:   "[...] and [...]",
		},
		{
			// Degenerate input: answers are contractually free of "::", but if a
			// third "::" appears, everything after the first separator is the hint.
			name:   "extra colon-pairs fold into hint (back reveals answer only)",
			tmpl:   "{{cloze:Text}}",
			fields: map[string]string{"Text": "{{c1::a::b::c}}"},
			opts:   Opts{ClozeOrd: 1, Back: true},
			want:   "a",
		},
		{
			name:   "extra colon-pairs fold into hint (front shows full hint)",
			tmpl:   "{{cloze:Text}}",
			fields: map[string]string{"Text": "{{c1::a::b::c}}"},
			opts:   Opts{ClozeOrd: 1, Back: false},
			want:   "[b::c]",
		},
		{
			// Single colon inside the answer is preserved (only "::" splits).
			name:   "single colon in answer preserved on back",
			tmpl:   "{{cloze:Text}}",
			fields: map[string]string{"Text": "{{c1::a:b}}"},
			opts:   Opts{ClozeOrd: 1, Back: true},
			want:   "a:b",
		},
		{
			name:   "empty answer masks to ellipsis on front",
			tmpl:   "{{cloze:Text}}",
			fields: map[string]string{"Text": "x{{c1::}}y"},
			opts:   Opts{ClozeOrd: 1, Back: false},
			want:   "x[...]y",
		},
		{
			name:   "empty answer reveals to nothing on back",
			tmpl:   "{{cloze:Text}}",
			fields: map[string]string{"Text": "x{{c1::}}y"},
			opts:   Opts{ClozeOrd: 1, Back: true},
			want:   "xy",
		},
		{
			name:   "multi-line answer masked on front",
			tmpl:   "{{cloze:Text}}",
			fields: map[string]string{"Text": "{{c1::line one\nline two}}"},
			opts:   Opts{ClozeOrd: 1, Back: false},
			want:   "[...]",
		},
		{
			name:   "multi-line answer revealed on back",
			tmpl:   "{{cloze:Text}}",
			fields: map[string]string{"Text": "{{c1::line one\nline two}}"},
			opts:   Opts{ClozeOrd: 1, Back: true},
			want:   "line one\nline two",
		},
		{
			name:   "ordinal zero never active, always revealed",
			tmpl:   "{{cloze:Text}}",
			fields: map[string]string{"Text": "{{c0::shown}} {{c1::hidden}}"},
			opts:   Opts{ClozeOrd: 1, Back: false},
			want:   "shown [...]",
		},
		{
			// Non-greedy match: answer="out{{c2", hint="in", consuming through the
			// first "}}"; the remaining "}}" is left as literal text. Back reveals
			// the answer, so "out{{c2" + trailing "}}".
			name:   "nested braces match first close (documents current behavior)",
			tmpl:   "{{cloze:Text}}",
			fields: map[string]string{"Text": "{{c1::out{{c2::in}}}}"},
			opts:   Opts{ClozeOrd: 1, Back: true},
			want:   "out{{c2}}",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := Render(tt.tmpl, tt.fields, tt.opts)
			if err != nil {
				t.Fatalf("Render() unexpected error: %v", err)
			}
			if got != tt.want {
				t.Errorf("Render() = %q, want %q", got, tt.want)
			}
		})
	}
}
