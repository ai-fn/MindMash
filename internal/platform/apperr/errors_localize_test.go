package apperr_test

import (
	"strings"
	"testing"

	"golang.org/x/text/language"

	"github.com/mindmesh/mindmesh/internal/platform/apperr"
	"github.com/mindmesh/mindmesh/internal/platform/i18n"
)

// External test package (apperr_test) so it can import platform/i18n, which itself depends on
// platform/apperr — importing it from the in-package test would be an import cycle.

func TestAppErrorLocalize(t *testing.T) {
	tagged := apperr.Unauthenticated("invalid credentials").
		WithMsgID(i18n.MsgInvalidCredentials)

	if got := tagged.Localize(i18n.Printer(language.Russian)); !strings.Contains(got, "Не удалось войти") {
		t.Errorf("Russian localize = %q, want Russian text", got)
	}
	if got := tagged.Localize(i18n.Printer(language.Chinese)); !strings.Contains(got, "无法登录") {
		t.Errorf("Chinese localize = %q, want Chinese text", got)
	}

	// A nil printer (no resolved locale) falls back to the plain English Message.
	if got := tagged.Localize(nil); got != "invalid credentials" {
		t.Errorf("nil-printer localize = %q, want plain message", got)
	}

	// An error without a MsgID is never localized, even with a printer.
	plain := apperr.Internal("boom")
	if got := plain.Localize(i18n.Printer(language.Russian)); got != "boom" {
		t.Errorf("un-tagged localize = %q, want plain message unchanged", got)
	}
}
