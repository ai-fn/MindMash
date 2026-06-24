// Package searchdsl parses the Anki-like card/note search query language into a
// boolean AST. The AST is the package's public contract; translating it into a
// concrete backend query (e.g. OpenSearch) is the caller's responsibility, so
// the parser stays pure and independently testable.
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

// Term is a single search clause. Field is the token prefix before the first
// colon ("deck", "tag", ...) or empty for a bare full-text term. Value is the
// remainder (which may itself contain colons, e.g. a nested deck path).
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

// Or is a disjunction: any child may match. The "OR" keyword (case-insensitive)
// separates Or branches and binds looser than implicit AND, so "a b OR c"
// parses as (a AND b) OR c.
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

// tokenize splits the input on whitespace, except whitespace inside double
// quotes, which is preserved so a quoted phrase stays a single token. The quote
// characters are kept in the token; parseClause interprets and strips them.
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

// parseClause parses a single token into a leaf Node. A field prefix is split
// off at the first colon, unless the token is itself a quoted phrase. A "prop:"
// clause becomes a Prop comparison; a double-quoted value (bare or after a field
// prefix) becomes a Phrase Term with the quotes stripped; everything else is a
// plain Term.
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
		name := s[:i]
		numStr := s[i+len(op):]
		if name == "" {
			return nil, fmt.Errorf("searchdsl: prop missing field name in %q", s)
		}
		n, err := strconv.Atoi(numStr)
		if err != nil {
			return nil, fmt.Errorf("searchdsl: prop %q has invalid number %q", name, numStr)
		}
		return Prop{Name: name, Op: op, Value: n}, nil
	}
	return nil, fmt.Errorf("searchdsl: prop %q has no comparison operator", s)
}
