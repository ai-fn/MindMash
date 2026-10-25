package identity

import (
	"net/mail"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/mindmesh/mindmesh/internal/platform/apperr"
	"github.com/mindmesh/mindmesh/internal/platform/i18n"
)

const (
	maxEmailLen       = 254 // RFC 5321 path limit
	minPasswordRunes  = 8
	maxPasswordBytes  = 128 // bounds request cost; argon2 input is not truncated
	maxProfileNameLen = 64
)

// normalizeEmail trims s and accepts only a bare address, not "Name <addr>".
func normalizeEmail(s string) (string, error) {
	s = strings.TrimSpace(s)
	if s == "" || len(s) > maxEmailLen {
		return "", apperr.InvalidArgument("invalid email")
	}
	addr, err := mail.ParseAddress(s)
	if err != nil || addr.Address != s {
		return "", apperr.InvalidArgument("invalid email")
	}
	if _, domain, _ := strings.Cut(s, "@"); !strings.Contains(domain, ".") {
		return "", apperr.InvalidArgument("invalid email")
	}
	return s, nil
}

func validatePassword(p string) error {
	if len(p) > maxPasswordBytes || !utf8.ValidString(p) {
		return apperr.InvalidArgument("password is too long")
	}
	if utf8.RuneCountInString(p) < minPasswordRunes {
		return apperr.InvalidArgument("password must be at least 8 characters")
	}
	return nil
}

// normalizeLocale maps any input to a supported base locale, defaulting to English.
func normalizeLocale(s string) string {
	if s == "" {
		return "en"
	}
	base, _ := i18n.ResolveTag(s, "").Base()
	return base.String()
}

func normalizeProfileName(s string) (string, error) {
	s = strings.TrimSpace(s)
	if s == "" {
		return "", apperr.InvalidArgument("profile name is required")
	}
	if utf8.RuneCountInString(s) > maxProfileNameLen || strings.ContainsFunc(s, unicode.IsControl) {
		return "", apperr.InvalidArgument("invalid profile name")
	}
	return s, nil
}
