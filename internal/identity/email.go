package identity

import (
	"context"

	"go.uber.org/zap"
)

// LogEmailSender is the development stub for EmailSender.  It logs the email
// and token at info level rather than dispatching real email — sufficient for
// local development and unit tests without an SMTP dependency.
type LogEmailSender struct {
	log *zap.Logger
}

// NewLogEmailSender returns an EmailSender that writes to the given logger.
func NewLogEmailSender(log *zap.Logger) *LogEmailSender {
	return &LogEmailSender{log: log}
}

// SendVerification logs the verification token instead of sending an email.
func (s *LogEmailSender) SendVerification(_ context.Context, email, token string) error {
	s.log.Info("identity: send verification email (stub)",
		zap.String("email", email),
		zap.String("token", token),
	)
	return nil
}

// SendPasswordReset logs the reset token instead of sending an email.
func (s *LogEmailSender) SendPasswordReset(_ context.Context, email, token string) error {
	s.log.Info("identity: send password-reset email (stub)",
		zap.String("email", email),
		zap.String("token", token),
	)
	return nil
}
