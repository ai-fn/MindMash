// Package template renders {{Field}}, {{#F}}/{{^F}} sections, {{FrontSide}} and {{cloze:F}} by pure
// substitution. Fields pass through unescaped: the client's sandboxed iframe sanitizes and runs JS.
package template

import (
	"errors"
	"fmt"
	"strings"
)

// ErrMalformedTemplate is returned (wrapped) when a template cannot be parsed,
// e.g. an unclosed section or a mismatched closing tag.
var ErrMalformedTemplate = errors.New("template: malformed template")

// Opts carries render context.
type Opts struct {
	// FrontSide is the already-rendered front side, substituted for {{FrontSide}}
	// on the back. Empty string if none was provided.
	FrontSide string
	// ClozeOrd is the active cloze ordinal (1-based). 0 means "no active cloze
	// ordinal": all cloze deletions reveal their plain answer text.
	ClozeOrd int
	// Back is true when rendering the back/answer side. It affects cloze reveal
	// and is the side on which {{FrontSide}} is meaningful.
	Back bool
}

// Render renders tmpl against the given fields (name -> value) with opts.
// It returns ErrMalformedTemplate (wrapped) for unparseable templates.
func Render(tmpl string, fields map[string]string, opts Opts) (string, error) {
	nodes, rest, err := parse(tmpl, "")
	if err != nil {
		return "", err
	}
	if rest != "" {
		// A closing tag with no matching open section was left over.
		return "", fmt.Errorf("%w: unexpected %q", ErrMalformedTemplate, rest)
	}
	var sb strings.Builder
	renderNodes(&sb, nodes, fields, opts)
	return sb.String(), nil
}

// node is a parsed template element.
type node struct {
	kind    nodeKind
	text    string // literal text (kindText) or field/tag name (others)
	inverse bool   // for kindSection: true when {{^...}}
	cloze   bool   // for kindTag: true when {{cloze:Name}}
	kids    []node // for kindSection
}

type nodeKind int

const (
	kindText    nodeKind = iota // literal text
	kindTag                     // {{Name}}, {{cloze:Name}}, {{FrontSide}}
	kindSection                 // {{#Name}}...{{/Name}} or {{^Name}}...{{/Name}}
)

const (
	openTag  = "{{"
	closeTag = "}}"
)

// parse reads until stopTag's close tag (or EOF when stopTag is "") and returns the rest of src.
func parse(src, stopTag string) ([]node, string, error) {
	var nodes []node
	for {
		i := strings.Index(src, openTag)
		if i < 0 {
			// No more tags. If we were expecting a closing tag, that's an error.
			if stopTag != "" {
				return nil, "", fmt.Errorf("%w: unclosed section %q", ErrMalformedTemplate, stopTag)
			}
			if src != "" {
				nodes = append(nodes, node{kind: kindText, text: src})
			}
			return nodes, "", nil
		}
		if i > 0 {
			nodes = append(nodes, node{kind: kindText, text: src[:i]})
		}
		j := strings.Index(src[i:], closeTag)
		if j < 0 {
			return nil, "", fmt.Errorf("%w: unterminated tag", ErrMalformedTemplate)
		}
		inner := src[i+len(openTag) : i+j]
		afterTag := src[i+j+len(closeTag):]
		// A second "{{" before the closing "}}" means an unterminated/nested tag;
		// reject it rather than silently truncating at the first "}}".
		if strings.Contains(inner, openTag) {
			return nil, "", fmt.Errorf("%w: nested or unterminated tag near %q", ErrMalformedTemplate, openTag+inner)
		}
		name := strings.TrimSpace(inner)
		if name == "" {
			return nil, "", fmt.Errorf("%w: empty tag", ErrMalformedTemplate)
		}

		switch {
		case strings.HasPrefix(name, "#"), strings.HasPrefix(name, "^"):
			inverse := name[0] == '^'
			secName := strings.TrimSpace(name[1:])
			if secName == "" {
				return nil, "", fmt.Errorf("%w: empty section name", ErrMalformedTemplate)
			}
			kids, rest, err := parse(afterTag, secName)
			if err != nil {
				return nil, "", err
			}
			nodes = append(nodes, node{kind: kindSection, text: secName, inverse: inverse, kids: kids})
			src = rest
		case strings.HasPrefix(name, "/"):
			closeName := strings.TrimSpace(name[1:])
			if closeName == "" {
				return nil, "", fmt.Errorf("%w: empty closing tag name", ErrMalformedTemplate)
			}
			if stopTag == "" {
				return nil, "", fmt.Errorf("%w: closing tag %q without matching section", ErrMalformedTemplate, closeName)
			}
			if closeName != stopTag {
				return nil, "", fmt.Errorf("%w: mismatched closing tag: expected %q, got %q", ErrMalformedTemplate, stopTag, closeName)
			}
			return nodes, afterTag, nil
		case strings.HasPrefix(name, "cloze:"):
			field := strings.TrimSpace(name[len("cloze:"):])
			if field == "" {
				return nil, "", fmt.Errorf("%w: empty cloze field name", ErrMalformedTemplate)
			}
			nodes = append(nodes, node{kind: kindTag, text: field, cloze: true})
			src = afterTag
		default:
			nodes = append(nodes, node{kind: kindTag, text: name})
			src = afterTag
		}
	}
}

func renderNodes(sb *strings.Builder, nodes []node, fields map[string]string, opts Opts) {
	for _, n := range nodes {
		switch n.kind {
		case kindText:
			sb.WriteString(n.text)
		case kindTag:
			switch {
			case n.cloze:
				sb.WriteString(renderClozeField(fields[n.text], opts))
			case n.text == "FrontSide":
				sb.WriteString(opts.FrontSide)
			default:
				// Unknown field -> empty string (Anki-compatible).
				sb.WriteString(fields[n.text])
			}
		case kindSection:
			show := nonEmpty(fields, n.text)
			if n.inverse {
				show = !show
			}
			if show {
				renderNodes(sb, n.kids, fields, opts)
			}
		}
	}
}

// nonEmpty reports whether name exists in fields and its value is not "".
// Whitespace-only values count as non-empty (matching Anki; no trimming).
func nonEmpty(fields map[string]string, name string) bool {
	v, ok := fields[name]
	return ok && v != ""
}
