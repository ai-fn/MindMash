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

func (s *LogEmailSender) SendVerification(_ context.Context, email, token, locale string) error {
	s.logMessage("verification", email, token, locale, i18n.MsgVerificationSubject, i18n.MsgVerificationBody)
	return nil
}

func (s *LogEmailSender) SendPasswordReset(_ context.Context, email, token, locale string) error {
	s.logMessage("password-reset", email, token, locale, i18n.MsgPasswordResetSubject, i18n.MsgPasswordResetBody)
	return nil
}

func (s *LogEmailSender) logMessage(kind, email, token, locale, subjectID, bodyID string) {
	if !s.logSecrets {
		s.log.Warn("identity: email stub dropped a "+kind+" message; configure a real sender", zap.String("locale", locale))
		return
	}
	p := i18n.Printer(i18n.ResolveTag(locale, ""))
	s.log.Info("identity: send "+kind+" email (stub)",
		zap.String("email", email),
		zap.String("token", token),
		zap.String("locale", locale),
		zap.String("subject", p.Sprintf(subjectID)),
		zap.String("body", p.Sprintf(bodyID, token)),
	)
}
