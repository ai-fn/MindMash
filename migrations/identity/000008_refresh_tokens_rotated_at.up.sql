-- Set only by rotation; lets a second request holding the same token (two tabs) succeed inside a short grace window.
ALTER TABLE refresh_tokens ADD COLUMN rotated_at TIMESTAMPTZ;
