-- Note type (model) definition: fields schema, card templates, and CSS.
-- fields and card_templates are stored as JSONB for flexible schema evolution.
-- Distributed by user_id (Citus shard key); composite PK (user_id, id).
CREATE TABLE note_types (
    id             UUID          NOT NULL,
    user_id        UUID          NOT NULL,
    profile_id     UUID          NOT NULL,          -- carried from Identity; no FK cross-service
    name           TEXT          NOT NULL,
    fields         JSONB         NOT NULL DEFAULT '[]', -- ordered list of field descriptors
    card_templates JSONB         NOT NULL DEFAULT '[]', -- ordered list of template objects
    css            TEXT          NOT NULL DEFAULT '',
    usn            BIGINT        NOT NULL DEFAULT 0,
    mtime          TIMESTAMPTZ   NOT NULL DEFAULT now(),
    PRIMARY KEY (user_id, id),
    -- fields and card_templates are ordered lists; reject non-array JSON.
    CHECK (jsonb_typeof(fields) = 'array'),
    CHECK (jsonb_typeof(card_templates) = 'array')
);
