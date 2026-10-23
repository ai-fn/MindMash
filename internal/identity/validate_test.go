package identity

import (
	"strings"
	"testing"
)

func TestNormalizeEmail(t *testing.T) {
	long := strings.Repeat("a", 250) + "@x.io"
	tests := []struct {
		in, want string
		ok       bool
	}{
		{"alice@example.com", "alice@example.com", true},
		{"  alice@example.com\t", "alice@example.com", true},
		{"", "", false},
		{"   ", "", false},
		{"alice", "", false},
		{"alice@localhost", "", false},
		{"Alice <alice@example.com>", "", false},
		{"a@b.com, c@d.com", "", false},
		{long, "", false},
	}
	for _, tc := range tests {
		got, err := normalizeEmail(tc.in)
		if (err == nil) != tc.ok || got != tc.want {
			t.Errorf("normalizeEmail(%q) = %q, %v; want %q ok=%v", tc.in, got, err, tc.want, tc.ok)
		}
	}
}

func TestValidatePassword(t *testing.T) {
	tests := []struct {
		name string
		in   string
		ok   bool
	}{
		{"min length", "12345678", true},
		{"too short", "1234567", false},
		{"counts runes not bytes", "пароль12", true},
		{"short multibyte", "пароль1", false},
		{"max length", strings.Repeat("a", maxPasswordBytes), true},
		{"too long", strings.Repeat("a", maxPasswordBytes+1), false},
		{"invalid utf8", "abcdefg\xff\xfe", false},
	}
	for _, tc := range tests {
		if err := validatePassword(tc.in); (err == nil) != tc.ok {
			t.Errorf("%s: validatePassword err=%v, want ok=%v", tc.name, err, tc.ok)
		}
	}
}

func TestNormalizeLocale(t *testing.T) {
	tests := map[string]string{
		"":        "en",
		"en":      "en",
		"ru":      "ru",
		"ru-RU":   "ru",
		"zh-Hans": "zh",
		"fr":      "en",
		"???":     "en",
	}
	for in, want := range tests {
		if got := normalizeLocale(in); got != want {
			t.Errorf("normalizeLocale(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestNormalizeProfileName(t *testing.T) {
	tests := []struct {
		in, want string
		ok       bool
	}{
		{"Work", "Work", true},
		{"  Work  ", "Work", true},
		{"", "", false},
		{"   ", "", false},
		{"a\nb", "", false},
		{"a\x00b", "", false},
		{strings.Repeat("я", maxProfileNameLen), strings.Repeat("я", maxProfileNameLen), true},
		{strings.Repeat("я", maxProfileNameLen+1), "", false},
	}
	for _, tc := range tests {
		got, err := normalizeProfileName(tc.in)
		if (err == nil) != tc.ok || got != tc.want {
			t.Errorf("normalizeProfileName(%q) = %q, %v; want %q ok=%v", tc.in, got, err, tc.want, tc.ok)
		}
	}
}
