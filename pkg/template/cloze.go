package template

import (
	"maps"
	"regexp"
	"slices"
	"strconv"
)

// clozeRe matches {{cN::answer}} or {{cN::answer::hint}}; (?s) because HTML answers often span lines.
var clozeRe = regexp.MustCompile(`(?s)\{\{c(\d+)::(.*?)(?:::(.*?))?\}\}`)

// ClozeIndices returns the sorted distinct N of {{cN::}}; c0 (ClozeOrd 0 means "reveal all") and
// int overflows are ignored.
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

// renderClozeField masks only the active ordinal on the front, as "[...]" or "[hint]"; everything
// else shows the answer.
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
