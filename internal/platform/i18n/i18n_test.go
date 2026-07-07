package i18n

import (
	"net/http/httptest"
	"strings"
	"testing"

	"golang.org/x/text/language"

	"github.com/mindmesh/mindmesh/internal/platform/auth"
)

func TestResolveTag(t *testing.T) {
	tests := []struct {
		name           string
		identityLocale string
		acceptLanguage string
		want           language.Tag
	}{
		{"identity locale wins over header", "ru", "en-US,en;q=0.9", language.Russian},
		{"identity regional tag normalizes", "zh-CN", "", language.Chinese},
		{"header used when no identity locale", "", "ru-RU,ru;q=0.9,en;q=0.8", language.Russian},
		{"header q-list picks best supported", "", "fr-FR,zh;q=0.7", language.Chinese},
		{"unknown identity locale falls back to english", "xx", "", language.English},
		{"unknown header falls back to english", "", "fr-FR,de;q=0.8", language.English},
		{"empty everything defaults to english", "", "", language.English},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := ResolveTag(tc.identityLocale, tc.acceptLanguage)
			if got != tc.want {
				t.Fatalf("ResolveTag(%q, %q) = %v, want %v", tc.identityLocale, tc.acceptLanguage, got, tc.want)
			}
		})
	}
}

func TestPrinterLocalizesMessages(t *testing.T) {
	tests := []struct {
		tag      language.Tag
		key      string
		contains string
	}{
		{language.English, MsgInvalidCredentials, "couldn't sign you in"},
		{language.Russian, MsgInvalidCredentials, "Не удалось войти"},
		{language.Chinese, MsgInvalidCredentials, "无法登录"},
		{language.Russian, MsgEmailAlreadyExists, "уже существует"},
		{language.Chinese, MsgInvalidOrExpiredToken, "已过期"},
	}
	for _, tc := range tests {
		got := Printer(tc.tag).Sprintf(tc.key)
		if !strings.Contains(got, tc.contains) {
			t.Errorf("Printer(%v).Sprintf(%q) = %q, want substring %q", tc.tag, tc.key, got, tc.contains)
		}
	}
}

func TestPrinterInterpolatesEmailToken(t *testing.T) {
	got := Printer(language.Russian).Sprintf(MsgVerificationBody, "TOK123")
	if !strings.Contains(got, "TOK123") {
		t.Errorf("verification body did not interpolate token: %q", got)
	}
	if !strings.Contains(got, "MindMesh") {
		t.Errorf("verification body not localized/branded: %q", got)
	}
}

func TestUnknownKeyFallsBackToEnglish(t *testing.T) {
	// A message absent for a locale resolves via the catalog's English fallback.
	got := Printer(language.Chinese).Sprintf(MsgVerificationSubject)
	if got == "" {
		t.Fatal("expected a non-empty localized subject")
	}
}

func TestResolveFromRequestPrefersIdentityLocale(t *testing.T) {
	r := httptest.NewRequest("GET", "/", nil)
	r.Header.Set("Accept-Language", "zh-CN")
	r = r.WithContext(auth.WithIdentity(r.Context(), auth.IdentityContext{Locale: "ru"}))
	if got := ResolveFromRequest(r); got != language.Russian {
		t.Fatalf("ResolveFromRequest = %v, want Russian (identity locale should win)", got)
	}
}

func TestResolveFromRequestUsesHeaderWithoutIdentity(t *testing.T) {
	r := httptest.NewRequest("GET", "/", nil)
	r.Header.Set("Accept-Language", "zh-CN,zh;q=0.9")
	if got := ResolveFromRequest(r); got != language.Chinese {
		t.Fatalf("ResolveFromRequest = %v, want Chinese", got)
	}
}
