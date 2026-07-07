package identity

import (
	"context"

	"go.uber.org/zap"

	"github.com/mindmesh/mindmesh/internal/platform/i18n"
)

// LogEmailSender is the development stub for EmailSender.  It renders the localized subject and
// body (from the recipient's locale) and logs them rather than dispatching real email —
// sufficient for local development and unit tests without an SMTP dependency.
type LogEmailSender struct {
	log *zap.Logger
}

// NewLogEmailSender returns an EmailSender that writes to the given logger.
func NewLogEmailSender(log *zap.Logger) *LogEmailSender {
	return &LogEmailSender{log: log}
}

func (s *LogEmailSender) SendVerification(_ context.Context, email, token, locale string) error {
	p := i18n.Printer(i18n.ResolveTag(locale, ""))
	s.log.Info("identity: send verification email (stub)",
		zap.String("email", email),
		zap.String("token", token),
		zap.String("locale", locale),
		zap.String("subject", p.Sprintf(i18n.MsgVerificationSubject)),
		zap.String("body", p.Sprintf(i18n.MsgVerificationBody, token)),
	)
	return nil
}

func (s *LogEmailSender) SendPasswordReset(_ context.Context, email, token, locale string) error {
	p := i18n.Printer(i18n.ResolveTag(locale, ""))
	s.log.Info("identity: send password-reset email (stub)",
		zap.String("email", email),
		zap.String("token", token),
		zap.String("locale", locale),
		zap.String("subject", p.Sprintf(i18n.MsgPasswordResetSubject)),
		zap.String("body", p.Sprintf(i18n.MsgPasswordResetBody, token)),
	)
	return nil
}
