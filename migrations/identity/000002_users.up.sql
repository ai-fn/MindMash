CREATE TABLE IF NOT EXISTS users (
    id             UUID PRIMARY KEY,
    email          CITEXT NOT NULL UNIQUE,
    email_verified BOOLEAN NOT NULL DEFAULT FALSE,
    password_hash  TEXT,                                -- nullable: OAuth-only accounts have no password
    locale         TEXT NOT NULL DEFAULT 'en',
    status         TEXT NOT NULL DEFAULT 'active',      -- active | suspended | deleted
    created_at     TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at     TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE INDEX IF NOT EXISTS idx_users_status ON users (status);
