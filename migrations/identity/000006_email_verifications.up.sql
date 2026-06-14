CREATE TABLE email_verifications (
    token       TEXT PRIMARY KEY,           -- opaque random token (also used for password reset)
    user_id     UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    purpose     TEXT NOT NULL DEFAULT 'verify_email',  -- verify_email | password_reset
    expires_at  TIMESTAMPTZ NOT NULL,
    consumed_at TIMESTAMPTZ,
    created_at  TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE INDEX idx_email_verifications_user_id ON email_verifications (user_id);
CREATE INDEX idx_email_verifications_expires_at ON email_verifications (expires_at);
