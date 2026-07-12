CREATE TABLE IF NOT EXISTS profiles (
    id         UUID PRIMARY KEY,
    user_id    UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    name       TEXT NOT NULL,
    is_default BOOLEAN NOT NULL DEFAULT FALSE,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE INDEX IF NOT EXISTS idx_profiles_user_id ON profiles (user_id);

-- at most one default profile per user
CREATE UNIQUE INDEX IF NOT EXISTS idx_profiles_one_default ON profiles (user_id) WHERE is_default;
