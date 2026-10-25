package identity

import (
	"context"

	"go.uber.org/zap"

	"github.com/mindmesh/mindmesh/internal/platform/i18n"
)

// LogEmailSender is the development stub for EmailSender: it logs the localized message
// instead of sending it. Tokens and addresses are logged only when logSecrets is set.
type LogEmailSender struct {
	log        *zap.Logger
	logSecrets bool
}

func NewLogEmailSender(log *zap.Logger, logSecrets bool) *LogEmailSender {
	return &LogEmailSender{log: log, logSecrets: logSecrets}
}

type emailTemplate struct{ kind, subjectID, bodyID string }

var (
	verificationEmail  = emailTemplate{"verification", i18n.MsgVerificationSubject, i18n.MsgVerificationBody}
	passwordResetEmail = emailTemplate{"password-reset", i18n.MsgPasswordResetSubject, i18n.MsgPasswordResetBody}
)

func (s *LogEmailSender) SendVerification(_ context.Context, email, token, locale string) error {
	s.logMessage(verificationEmail, email, token, locale)
	return nil
}

func (s *LogEmailSender) SendPasswordReset(_ context.Context, email, token, locale string) error {
	s.logMessage(passwordResetEmail, email, token, locale)
	return nil
}

func (s *LogEmailSender) logMessage(t emailTemplate, email, token, locale string) {
	if !s.logSecrets {
		s.log.Warn("identity: email stub dropped a "+t.kind+" message; configure a real sender", zap.String("locale", locale))
		return
	}
	p := i18n.Printer(i18n.ResolveTag(locale, ""))
	s.log.Info("identity: send "+t.kind+" email (stub)",
		zap.String("email", email),
		zap.String("token", token),
		zap.String("locale", locale),
		zap.String("subject", p.Sprintf(t.subjectID)),
		zap.String("body", p.Sprintf(t.bodyID, token)),
	)
}
