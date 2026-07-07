package identity

import (
	"context"
	"strings"
	"testing"

	"go.uber.org/zap"
	"go.uber.org/zap/zaptest/observer"
)

// fieldsOf returns the structured fields of the single logged entry as a map.
func fieldsOf(t *testing.T, logs *observer.ObservedLogs) map[string]any {
	t.Helper()
	entries := logs.All()
	if len(entries) != 1 {
		t.Fatalf("expected exactly one log entry, got %d", len(entries))
	}
	return entries[0].ContextMap()
}

func TestLogEmailSenderLocalizesVerification(t *testing.T) {
	tests := []struct {
		locale        string
		subjectSubstr string
		bodySubstr    string
	}{
		{"en", "Verify your MindMesh email", "Confirm your email"},
		{"ru", "Подтвердите", "Подтвердите почту"},
		{"zh", "验证", "确认邮箱"},
	}
	for _, tc := range tests {
		t.Run(tc.locale, func(t *testing.T) {
			core, logs := observer.New(zap.InfoLevel)
			sender := NewLogEmailSender(zap.New(core))

			if err := sender.SendVerification(context.Background(), "rin@example.com", "TOK123", tc.locale); err != nil {
				t.Fatalf("SendVerification: %v", err)
			}

			fields := fieldsOf(t, logs)
			subject, _ := fields["subject"].(string)
			body, _ := fields["body"].(string)
			if !strings.Contains(subject, tc.subjectSubstr) {
				t.Errorf("subject %q missing %q", subject, tc.subjectSubstr)
			}
			if !strings.Contains(body, tc.bodySubstr) {
				t.Errorf("body %q missing %q", body, tc.bodySubstr)
			}
			if !strings.Contains(body, "TOK123") {
				t.Errorf("body %q did not interpolate the token", body)
			}
		})
	}
}

func TestLogEmailSenderLocalizesPasswordReset(t *testing.T) {
	core, logs := observer.New(zap.InfoLevel)
	sender := NewLogEmailSender(zap.New(core))

	if err := sender.SendPasswordReset(context.Background(), "rin@example.com", "RST9", "ru"); err != nil {
		t.Fatalf("SendPasswordReset: %v", err)
	}

	fields := fieldsOf(t, logs)
	subject, _ := fields["subject"].(string)
	body, _ := fields["body"].(string)
	if !strings.Contains(subject, "Сброс пароля") {
		t.Errorf("subject %q not localized to Russian", subject)
	}
	if !strings.Contains(body, "RST9") {
		t.Errorf("body %q did not interpolate the token", body)
	}
}
