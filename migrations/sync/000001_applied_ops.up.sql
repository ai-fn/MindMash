-- sync_applied_ops dedups client-submitted op_ids for the /sync/apply push
-- path: a replay of a previously-applied op_id is recognized here instead of
-- being re-applied against Collection.
CREATE TABLE IF NOT EXISTS sync_applied_ops (
    user_id    UUID        NOT NULL,
    op_id      TEXT        NOT NULL,
    outcome    SMALLINT    NOT NULL,
    new_usn    BIGINT      NOT NULL DEFAULT 0,
    applied_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    PRIMARY KEY (user_id, op_id)
);
