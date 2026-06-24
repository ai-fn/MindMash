package searchdsl

import (
	"reflect"
	"testing"
)

func TestParseImplicitAnd(t *testing.T) {
	got, err := Parse("deck:Spanish tag:grammar is:due")
	if err != nil {
		t.Fatalf("Parse returned error: %v", err)
	}
	want := And{Children: []Node{
		Term{Field: "deck", Value: "Spanish"},
		Term{Field: "tag", Value: "grammar"},
		Term{Field: "is", Value: "due"},
	}}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("Parse = %#v, want %#v", got, want)
	}
}

func TestParseWildcard(t *testing.T) {
	tests := []struct {
		name string
		in   string
		want Node
	}{
		{"field wildcard", "front:hola*", Term{Field: "front", Value: "hola", Wildcard: true}},
		{"bare wildcard", "hol*", Term{Value: "hol", Wildcard: true}},
		{"no wildcard", "front:hola", Term{Field: "front", Value: "hola"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := Parse(tt.in)
			if err != nil {
				t.Fatalf("Parse(%q) returned error: %v", tt.in, err)
			}
			if !reflect.DeepEqual(got, tt.want) {
				t.Errorf("Parse(%q) = %#v, want %#v", tt.in, got, tt.want)
			}
		})
	}
}

func TestParseEmpty(t *testing.T) {
	for _, in := range []string{"", "   ", "\t"} {
		if _, err := Parse(in); err == nil {
			t.Errorf("Parse(%q) expected error for empty query, got nil", in)
		}
	}
}

func TestParseProp(t *testing.T) {
	tests := []struct {
		name string
		in   string
		want Node
	}{
		{"gte", "prop:ivl>=21", Prop{Name: "ivl", Op: ">=", Value: 21}},
		{"lte", "prop:reps<=5", Prop{Name: "reps", Op: "<=", Value: 5}},
		{"gt", "prop:lapses>0", Prop{Name: "lapses", Op: ">", Value: 0}},
		{"eq", "prop:ivl=1", Prop{Name: "ivl", Op: "=", Value: 1}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := Parse(tt.in)
			if err != nil {
				t.Fatalf("Parse(%q) returned error: %v", tt.in, err)
			}
			if !reflect.DeepEqual(got, tt.want) {
				t.Errorf("Parse(%q) = %#v, want %#v", tt.in, got, tt.want)
			}
		})
	}
}

func TestParsePropInvalid(t *testing.T) {
	for _, in := range []string{"prop:ivl", "prop:ivl>=", "prop:ivl>=abc", "prop:>=5"} {
		if _, err := Parse(in); err == nil {
			t.Errorf("Parse(%q) expected error, got nil", in)
		}
	}
}

func TestParsePhrase(t *testing.T) {
	tests := []struct {
		name string
		in   string
		want Node
	}{
		{"bare quoted phrase", `"exact phrase"`, Term{Value: "exact phrase", Phrase: true}},
		{"field quoted value", `front:"two words"`, Term{Field: "front", Value: "two words", Phrase: true}},
		{
			name: "phrase keeps internal spaces within implicit and",
			in:   `tag:x "a b"`,
			want: And{Children: []Node{
				Term{Field: "tag", Value: "x"},
				Term{Value: "a b", Phrase: true},
			}},
		},
		{"negated phrase", `-"no no"`, Not{Child: Term{Value: "no no", Phrase: true}}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := Parse(tt.in)
			if err != nil {
				t.Fatalf("Parse(%q) returned error: %v", tt.in, err)
			}
			if !reflect.DeepEqual(got, tt.want) {
				t.Errorf("Parse(%q) = %#v, want %#v", tt.in, got, tt.want)
			}
		})
	}
}

func TestParseOr(t *testing.T) {
	tests := []struct {
		name string
		in   string
		want Node
	}{
		{
			name: "simple or",
			in:   "tag:a OR tag:b",
			want: Or{Children: []Node{
				Term{Field: "tag", Value: "a"},
				Term{Field: "tag", Value: "b"},
			}},
		},
		{
			name: "lowercase or accepted",
			in:   "tag:a or tag:b",
			want: Or{Children: []Node{
				Term{Field: "tag", Value: "a"},
				Term{Field: "tag", Value: "b"},
			}},
		},
		{
			name: "and binds tighter than or",
			in:   "deck:S is:due OR flag:1",
			want: Or{Children: []Node{
				And{Children: []Node{
					Term{Field: "deck", Value: "S"},
					Term{Field: "is", Value: "due"},
				}},
				Term{Field: "flag", Value: "1"},
			}},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := Parse(tt.in)
			if err != nil {
				t.Fatalf("Parse(%q) returned error: %v", tt.in, err)
			}
			if !reflect.DeepEqual(got, tt.want) {
				t.Errorf("Parse(%q) = %#v, want %#v", tt.in, got, tt.want)
			}
		})
	}
}

func TestParseNegation(t *testing.T) {
	tests := []struct {
		name string
		in   string
		want Node
	}{
		{"negated clause", "-tag:grammar", Not{Child: Term{Field: "tag", Value: "grammar"}}},
		{"negated bare term", "-hola", Not{Child: Term{Value: "hola"}}},
		{
			name: "negation within implicit and",
			in:   "deck:Spanish -is:suspended",
			want: And{Children: []Node{
				Term{Field: "deck", Value: "Spanish"},
				Not{Child: Term{Field: "is", Value: "suspended"}},
			}},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := Parse(tt.in)
			if err != nil {
				t.Fatalf("Parse(%q) returned error: %v", tt.in, err)
			}
			if !reflect.DeepEqual(got, tt.want) {
				t.Errorf("Parse(%q) = %#v, want %#v", tt.in, got, tt.want)
			}
		})
	}
}

func TestParseSingleClause(t *testing.T) {
	tests := []struct {
		name string
		in   string
		want Node
	}{
		{"bare term", "hola", Term{Value: "hola"}},
		{"field term", "deck:Spanish", Term{Field: "deck", Value: "Spanish"}},
		{"nested deck path", "deck:Spanish::Verbs", Term{Field: "deck", Value: "Spanish::Verbs"}},
		{"tag", "tag:grammar", Term{Field: "tag", Value: "grammar"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := Parse(tt.in)
			if err != nil {
				t.Fatalf("Parse(%q) returned error: %v", tt.in, err)
			}
			if !reflect.DeepEqual(got, tt.want) {
				t.Errorf("Parse(%q) = %#v, want %#v", tt.in, got, tt.want)
			}
		})
	}
}
