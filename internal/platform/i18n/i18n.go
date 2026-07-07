// Package i18n provides backend message localization: a message catalog (English source with
// Russian/Chinese translations), locale negotiation, and request-scoped locale plumbing.
//
// It backs two surfaces (spec 13 build step 2): localized transactional emails (rendered from
// the recipient's stored `users.locale`) and localized user-facing error messages (rendered at
// the HTTP edge from the request's resolved locale). Precedence for request locale is the JWT
// `locale` claim (authenticated calls) over the `Accept-Language` header (pre-auth calls) over
// the English default.
package i18n

import (
	"context"
	"net/http"

	"golang.org/x/text/language"
	"golang.org/x/text/message"
	"golang.org/x/text/message/catalog"

	"github.com/mindmesh/mindmesh/internal/platform/auth"
)

// Supported UI/message locales. English is first and is the fallback/default.
var supported = []language.Tag{language.English, language.Russian, language.Chinese}

// Message IDs — stable keys into the catalog below. User-facing error IDs are attached to
// AppErrors via (*AppError).WithMsgID and localized at the HTTP edge; email IDs are rendered
// directly by the email sender. Keep the error strings aligned with the frontend catalogs
// (web/src/locales/*/auth.json) where they surface the same condition.
const (
	MsgInvalidCredentials    = "error.invalid_credentials"
	MsgEmailAlreadyExists    = "error.email_already_exists"
	MsgInvalidOrExpiredToken = "error.invalid_or_expired_token"

	MsgVerificationSubject  = "email.verification.subject"
	MsgVerificationBody     = "email.verification.body"
	MsgPasswordResetSubject = "email.password_reset.subject"
	MsgPasswordResetBody    = "email.password_reset.body"
)

var cat = buildCatalog()

func buildCatalog() catalog.Catalog {
	b := catalog.NewBuilder(catalog.Fallback(language.English))
	set := func(tag language.Tag, msgs map[string]string) {
		for key, msg := range msgs {
			// SetString only errors on a malformed message; our messages are static literals.
			_ = b.SetString(tag, key, msg)
		}
	}

	set(language.English, map[string]string{
		MsgInvalidCredentials:    "We couldn't sign you in. Check your email and password and try again.",
		MsgEmailAlreadyExists:    "An account with that email already exists.",
		MsgInvalidOrExpiredToken: "This link is invalid or has expired. Request a new one.",
		MsgVerificationSubject:   "Verify your MindMesh email",
		MsgVerificationBody:      "Welcome to MindMesh! Confirm your email with this token: %s",
		MsgPasswordResetSubject:  "Reset your MindMesh password",
		MsgPasswordResetBody:     "Use this token to reset your MindMesh password: %s",
	})
	set(language.Russian, map[string]string{
		MsgInvalidCredentials:    "Не удалось войти. Проверьте почту и пароль и попробуйте снова.",
		MsgEmailAlreadyExists:    "Аккаунт с такой почтой уже существует.",
		MsgInvalidOrExpiredToken: "Ссылка недействительна или устарела. Запросите новую.",
		MsgVerificationSubject:   "Подтвердите вашу почту в MindMesh",
		MsgVerificationBody:      "Добро пожаловать в MindMesh! Подтвердите почту с помощью этого токена: %s",
		MsgPasswordResetSubject:  "Сброс пароля MindMesh",
		MsgPasswordResetBody:     "Используйте этот токен для сброса пароля MindMesh: %s",
	})
	set(language.Chinese, map[string]string{
		MsgInvalidCredentials:    "无法登录。请检查邮箱和密码后重试。",
		MsgEmailAlreadyExists:    "该邮箱的账号已存在。",
		MsgInvalidOrExpiredToken: "此链接无效或已过期。请重新申请。",
		MsgVerificationSubject:   "验证你的 MindMesh 邮箱",
		MsgVerificationBody:      "欢迎使用 MindMesh！请使用此令牌确认邮箱：%s",
		MsgPasswordResetSubject:  "重置你的 MindMesh 密码",
		MsgPasswordResetBody:     "使用此令牌重置你的 MindMesh 密码：%s",
	})
	return b
}

var matcher = language.NewMatcher(supported)

// Printer returns a message printer bound to tag and the message catalog.
func Printer(tag language.Tag) *message.Printer {
	return message.NewPrinter(tag, message.Catalog(cat))
}

// ResolveTag negotiates a supported locale. The JWT `locale` claim (identityLocale) takes
// precedence when present; otherwise the Accept-Language header is honored; otherwise English.
func ResolveTag(identityLocale, acceptLanguage string) language.Tag {
	// matcher.Match returns the *input* tag decorated with region extensions (e.g.
	// "zh-u-rg-cnzzzz"); the second return value is the index into `supported`, which is what
	// we want — the clean base tag. A no-match yields index 0 (English), our default.
	if identityLocale != "" {
		tag, err := language.Parse(identityLocale)
		if err != nil {
			return language.English
		}
		_, idx, _ := matcher.Match(tag)
		return supported[idx]
	}
	tags, _, err := language.ParseAcceptLanguage(acceptLanguage)
	if err != nil || len(tags) == 0 {
		return language.English
	}
	_, idx, _ := matcher.Match(tags...)
	return supported[idx]
}

// ResolveFromRequest derives the locale for an HTTP request: the authenticated identity's
// locale (set upstream by the auth middleware) if present, else the Accept-Language header.
func ResolveFromRequest(r *http.Request) language.Tag {
	identityLocale := ""
	if id, ok := auth.IdentityFromContext(r.Context()); ok {
		identityLocale = id.Locale
	}
	return ResolveTag(identityLocale, r.Header.Get("Accept-Language"))
}

type ctxKey struct{}

// WithTag stores the resolved locale in ctx.
func WithTag(ctx context.Context, tag language.Tag) context.Context {
	return context.WithValue(ctx, ctxKey{}, tag)
}

// TagFromContext returns the locale stored by WithTag, or English if none.
func TagFromContext(ctx context.Context) language.Tag {
	if tag, ok := ctx.Value(ctxKey{}).(language.Tag); ok {
		return tag
	}
	return language.English
}

// PrinterFromContext returns a printer for the request-scoped locale.
func PrinterFromContext(ctx context.Context) *message.Printer {
	return Printer(TagFromContext(ctx))
}

// HTTPMiddleware resolves the request locale (identity claim → Accept-Language → English) and
// stores it in the request context for downstream handlers. Order it after the auth/identity
// middleware so the JWT locale claim is available to take precedence.
func HTTPMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		tag := ResolveFromRequest(r)
		next.ServeHTTP(w, r.WithContext(WithTag(r.Context(), tag)))
	})
}
