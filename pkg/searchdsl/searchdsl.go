// Package searchdsl parses the Anki-like search language into a boolean AST; callers translate it
// to a backend query.
package searchdsl

import (
	"fmt"
	"strconv"
	"strings"
	"unicode"
)

// Node is a parsed query expression: a Term leaf, or a boolean combinator
// (And, Or, Not) over child nodes.
type Node interface{ node() }

// Term splits at the first colon only, so Value may hold more (a nested deck path); Field "" is
// full-text.
type Term struct {
	Field string
	Value string
	// Phrase is set when the value was double-quoted, requesting an exact phrase
	// match rather than a tokenized term match.
	Phrase bool
	// Wildcard is set when the value had a trailing "*" (stripped from Value),
	// requesting a prefix match.
	Wildcard bool
}

func (Term) node() {}

// And is a conjunction: all children must match. Bare space-separated clauses
// combine into an And (implicit AND).
type And struct {
	Children []Node
}

func (And) node() {}

// Not negates its child clause (the leading "-" operator).
type Not struct {
	Child Node
}

func (Not) node() {}

// Or binds looser than implicit AND: "a b OR c" is (a AND b) OR c.
type Or struct {
	Children []Node
}

func (Or) node() {}

// Prop is a numeric comparison on a scheduling field, e.g. prop:ivl>=21. Op is
// one of ">=", "<=", ">", "<", "=".
type Prop struct {
	Name  string
	Op    string
	Value int
}

func (Prop) node() {}

// Parse turns a query string into a Node tree.
func Parse(input string) (Node, error) {
	tokens := tokenize(input)
	if len(tokens) == 0 {
		return nil, fmt.Errorf("searchdsl: empty query")
	}

	// Split into OR-separated groups; each group is an implicit-AND of clauses.
	var groups []Node
	var current []Node
	flush := func() {
		groups = append(groups, andOf(current))
		current = nil
	}
	for _, tok := range tokens {
		if strings.EqualFold(tok, "OR") {
			flush()
			continue
		}
		node, err := parseToken(tok)
		if err != nil {
			return nil, err
		}
		current = append(current, node)
	}
	flush()

	if len(groups) == 1 {
		return groups[0], nil
	}
	return Or{Children: groups}, nil
}

// andOf collapses a slice of clauses into a single Node: the lone clause when
// there is exactly one, otherwise an And.
func andOf(children []Node) Node {
	if len(children) == 1 {
		return children[0]
	}
	return And{Children: children}
}

// parseToken parses one whitespace-delimited token, peeling a leading "-"
// negation operator before parsing the underlying clause.
func parseToken(s string) (Node, error) {
	if neg, ok := strings.CutPrefix(s, "-"); ok && neg != "" {
		child, err := parseClause(neg)
		if err != nil {
			return nil, err
		}
		return Not{Child: child}, nil
	}
	return parseClause(s)
}

// tokenize keeps a quoted phrase as one token, quotes included; parseClause strips them.
func tokenize(s string) []string {
	var toks []string
	var b strings.Builder
	inQuote := false
	flush := func() {
		if b.Len() > 0 {
			toks = append(toks, b.String())
			b.Reset()
		}
	}
	for _, r := range s {
		switch {
		case r == '"':
			inQuote = !inQuote
			b.WriteRune(r)
		case unicode.IsSpace(r) && !inQuote:
			flush()
		default:
			b.WriteRune(r)
		}
	}
	flush()
	return toks
}

// parseClause doesn't split a quoted phrase at its colons.
func parseClause(s string) (Node, error) {
	field, value := "", s
	if !strings.HasPrefix(s, `"`) {
		if i := strings.IndexByte(s, ':'); i >= 0 {
			field, value = s[:i], s[i+1:]
		}
	}
	if field == "prop" {
		return parseProp(value)
	}
	if len(value) >= 2 && strings.HasPrefix(value, `"`) && strings.HasSuffix(value, `"`) {
		return Term{Field: field, Value: value[1 : len(value)-1], Phrase: true}, nil
	}
	if v, ok := strings.CutSuffix(value, "*"); ok {
		return Term{Field: field, Value: v, Wildcard: true}, nil
	}
	return Term{Field: field, Value: value}, nil
}

// propNames are the numeric card fields a prop clause may compare.
var propNames = map[string]bool{"ivl": true, "reps": true, "lapses": true}

// propOps lists the comparison operators a prop clause may use, longest first so
// ">=" is matched before ">".
var propOps = []string{">=", "<=", ">", "<", "="}

// parseProp parses the value of a prop clause ("ivl>=21") into a Prop.
func parseProp(s string) (Node, error) {
	for _, op := range propOps {
		i := strings.Index(s, op)
		if i < 0 {
			continue
		}
		name := strings.ToLower(s[:i])
		numStr := s[i+len(op):]
		if name == "" {
			return nil, fmt.Errorf("searchdsl: prop missing field name in %q", s)
		}
		if !propNames[name] {
			return nil, fmt.Errorf("searchdsl: unsupported prop %q", name)
		}
		n, err := strconv.Atoi(numStr)
		if err != nil {
			return nil, fmt.Errorf("searchdsl: prop %q has invalid number %q", name, numStr)
		}
		return Prop{Name: name, Op: op, Value: n}, nil
	}
	return nil, fmt.Errorf("searchdsl: prop %q has no comparison operator", s)
}
