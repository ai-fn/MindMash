package template

import (
	"maps"
	"regexp"
	"slices"
	"strconv"
)

// clozeRe matches a single cloze deletion: {{cN::answer}} or {{cN::answer::hint}}.
// Group 1 is the ordinal N, group 2 the answer, group 3 the optional hint.
// The answer is matched non-greedily and is assumed not to contain "::".
// The (?s) flag lets "." match newlines so multi-line answers/hints (common in
// HTML card content) are matched rather than silently passed through verbatim.
var clozeRe = regexp.MustCompile(`(?s)\{\{c(\d+)::(.*?)(?:::(.*?))?\}\}`)

// ClozeIndices returns the sorted, distinct cloze ordinal numbers (the N in
// {{cN::...}}) found across all the given field values. It drives Cloze card
// generation: one card per distinct ordinal. Returns nil when none are found.
//
// Ordinals are 1-based; "c0" and any digit run that overflows int are ignored
// (they cannot identify a valid card — ClozeOrd 0 means "reveal all").
func ClozeIndices(fields map[string]string) []int {
	seen := make(map[int]struct{})
	for _, v := range fields {
		for _, m := range clozeRe.FindAllStringSubmatch(v, -1) {
			n, err := strconv.Atoi(m[1])
			if err != nil || n == 0 {
				continue
			}
			seen[n] = struct{}{}
		}
	}
	if len(seen) == 0 {
		return nil
	}
	return slices.Sorted(maps.Keys(seen))
}

// renderClozeField renders a single cloze field value parameterized by the
// active ordinal (opts.ClozeOrd) and side (opts.Back).
//
//   - Active ordinal (== opts.ClozeOrd, when opts.ClozeOrd != 0) on the front:
//     masked to "[...]" (no hint) or "[hint]" (with hint).
//   - Every other case (active on the back, any non-active ordinal, and all
//     ordinals when opts.ClozeOrd == 0): revealed as the plain answer text
//     (hint dropped).
func renderClozeField(value string, opts Opts) string {
	return clozeRe.ReplaceAllStringFunc(value, func(match string) string {
		sub := clozeRe.FindStringSubmatch(match)
		ord, err := strconv.Atoi(sub[1])
		answer := sub[2]
		hint := sub[3]

		active := err == nil && opts.ClozeOrd != 0 && ord == opts.ClozeOrd
		if !active || opts.Back {
			// Reveal as plain answer text (hint dropped).
			return answer
		}
		// Active on the front: mask, showing the hint if present.
		if hint != "" {
			return "[" + hint + "]"
		}
		return "[...]"
	})
}
