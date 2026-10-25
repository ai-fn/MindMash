package collection

import (
	"fmt"
	"math"
	"strings"
	"unicode"

	"github.com/mindmesh/mindmesh/internal/platform/apperr"
	"github.com/mindmesh/mindmesh/internal/platform/validate"
)

const (
	maxNameRunes     = 255
	maxTemplateBytes = 64 << 10 // per template side, and CSS
	maxFields        = 64
	maxTemplates     = 64
	maxLanguageBytes = 35      // BCP-47 tag
	maxNoteBytes     = 1 << 20 // all field names and values together
	maxTags          = 100
	maxTagRunes      = 100
	maxTimezoneBytes = 64
	maxConfigListLen = 64 // FSRS weights, learning steps
)

func validatePage(p Page) error {
	return validate.OptionalID("page_token", p.Token)
}

func validateNoteType(name, css string, fields []Field, templates []CardTemplate) error {
	if err := validate.Name("name", name, maxNameRunes); err != nil {
		return err
	}
	if err := validate.Text("css", css, maxTemplateBytes); err != nil {
		return err
	}
	if err := validate.Count("fields", len(fields), maxFields); err != nil {
		return err
	}
	for i, f := range fields {
		if err := validate.Name(fmt.Sprintf("fields[%d].name", i), f.Name, maxNameRunes); err != nil {
			return err
		}
		if err := validate.Text(fmt.Sprintf("fields[%d].language", i), f.Language, maxLanguageBytes); err != nil {
			return err
		}
	}
	if err := validate.Count("card_templates", len(templates), maxTemplates); err != nil {
		return err
	}
	for i, t := range templates {
		if err := validate.Name(fmt.Sprintf("card_templates[%d].name", i), t.Name, maxNameRunes); err != nil {
			return err
		}
		if err := validate.Text(fmt.Sprintf("card_templates[%d].front", i), t.Front, maxTemplateBytes); err != nil {
			return err
		}
		if err := validate.Text(fmt.Sprintf("card_templates[%d].back", i), t.Back, maxTemplateBytes); err != nil {
			return err
		}
	}
	return nil
}

func validateNoteContent(fields map[string]string, tags []string) error {
	if err := validate.Count("fields", len(fields), maxFields); err != nil {
		return err
	}
	total := 0
	for k, v := range fields {
		if err := validate.Name("field name", k, maxNameRunes); err != nil {
			return err
		}
		if err := validate.Text("field value", v, maxNoteBytes); err != nil {
			return err
		}
		total += len(k) + len(v)
	}
	if total > maxNoteBytes {
		return apperr.InvalidArgument(fmt.Sprintf("fields must be at most %d bytes in total", maxNoteBytes))
	}
	if err := validate.Count("tags", len(tags), maxTags); err != nil {
		return err
	}
	for i, tag := range tags {
		if err := validateTag(fmt.Sprintf("tags[%d]", i), tag); err != nil {
			return err
		}
	}
	return nil
}

// validateTag also rejects whitespace: the search DSL splits tag:foo terms on it.
func validateTag(field, tag string) error {
	if err := validate.Name(field, tag, maxTagRunes); err != nil {
		return err
	}
	if strings.ContainsFunc(tag, unicode.IsSpace) {
		return apperr.InvalidArgument(field + " must not contain spaces")
	}
	return nil
}

func validateDeck(name, parentID, configID string) error {
	if err := validate.Name("name", name, maxNameRunes); err != nil {
		return err
	}
	if err := validate.OptionalID("parent_id", parentID); err != nil {
		return err
	}
	return validate.OptionalID("config_id", configID)
}

func validateDeckConfig(dc DeckConfig) error {
	if err := validate.Name("name", dc.Name, maxNameRunes); err != nil {
		return err
	}
	if err := validate.Text("timezone", dc.Timezone, maxTimezoneBytes); err != nil {
		return err
	}
	if err := validate.Count("fsrs_weights", len(dc.FSRSWeights), maxConfigListLen); err != nil {
		return err
	}
	return validate.Count("learning_steps", len(dc.LearningSteps), maxConfigListLen)
}

func validateSelector(sel SearchDocSelector) error {
	if err := validate.IDs("card_ids", sel.CardIDs); err != nil {
		return err
	}
	if err := validate.IDs("note_ids", sel.NoteIDs); err != nil {
		return err
	}
	return validate.IDs("deck_ids", sel.DeckIDs)
}

// validateFlags keeps flags within the SMALLINT column.
func validateFlags(flags int32) error {
	if flags < 0 || flags > math.MaxInt16 {
		return apperr.InvalidArgument(fmt.Sprintf("flags must be 0-%d", math.MaxInt16))
	}
	return nil
}
