-- Media metadata (blobs live in S3/MinIO; this is the authoritative index over them).
-- Distributed by user_id (Citus shard key) so a user's media reads/writes stay single-shard.
-- Dedupe is per-user: UNIQUE(user_id, checksum). The underlying blob is content-addressed and
-- shared across users (s3_key derives from the checksum), but each user keeps their own row so
-- ownership/link and per-user sync (usn/mtime) stay tenant-isolated.
CREATE TABLE IF NOT EXISTS media (
    id          UUID          NOT NULL,
    user_id     UUID          NOT NULL,
    profile_id  UUID          NOT NULL,
    checksum    BYTEA         NOT NULL,       -- SHA-256 of the original bytes
    kind        TEXT          NOT NULL,       -- image | audio | video | latex | other
    mime        TEXT          NOT NULL,
    s3_key      TEXT          NOT NULL,       -- content-addressed key, e.g. '<hex-checksum>.jpg'
    bytes       BIGINT        NOT NULL,
    status      TEXT          NOT NULL,       -- pending (presigned) | ready (confirmed)
    usn         BIGINT        NOT NULL,
    mtime       TIMESTAMPTZ   NOT NULL DEFAULT now(),
    created_at  TIMESTAMPTZ   NOT NULL DEFAULT now(),
    PRIMARY KEY (user_id, id),
    UNIQUE (user_id, checksum)
);

-- Sync delta feed reads rows above a client USN watermark; keep it single-shard-scannable.
CREATE INDEX IF NOT EXISTS media_user_usn ON media (user_id, usn);

-- Derived renditions (thumbnails, transcodes, rendered LaTeX). Carries user_id for Citus
-- colocation with the parent media row. Variant blobs are also content-addressed and shared.
CREATE TABLE IF NOT EXISTS media_variants (
    user_id   UUID   NOT NULL,
    media_id  UUID   NOT NULL,
    variant   TEXT   NOT NULL,        -- e.g. 'thumb'
    s3_key    TEXT   NOT NULL,        -- '<hex-checksum>/<variant>.<ext>'
    mime      TEXT   NOT NULL,
    width     INT,
    height    INT,
    PRIMARY KEY (user_id, media_id, variant)
);

-- Per-user monotonic USN counter (same mechanism as Collection). Each write bumps it so the
-- Sync change feed can order media alongside a user's other entities.
CREATE TABLE IF NOT EXISTS user_usn (
    user_id UUID   NOT NULL,
    usn     BIGINT NOT NULL,
    PRIMARY KEY (user_id)
);
