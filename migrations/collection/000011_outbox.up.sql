-- Transactional outbox for reliable event publishing to Kafka.
-- The relay polls rows WHERE published_at IS NULL, publishes them keyed by user_id,
-- then stamps published_at.  Written inside the same DB transaction as the domain change.
-- Distributed by user_id (Citus shard key); composite PK (user_id, id).
CREATE TABLE outbox (
    id             UUID          NOT NULL,
    user_id        UUID          NOT NULL,
    aggregate_type TEXT          NOT NULL,       -- e.g. 'card', 'deck', 'note'
    aggregate_id   UUID          NOT NULL,       -- id of the mutated entity
    event_type     TEXT          NOT NULL,       -- e.g. 'card.created', 'card.reviewed'
    payload        JSONB         NOT NULL,
    created_at     TIMESTAMPTZ   NOT NULL DEFAULT now(),
    published_at   TIMESTAMPTZ,                  -- NULL until the relay publishes to Kafka
    PRIMARY KEY (user_id, id)
);

-- Partial index for relay polling: only unpublished rows need to be scanned.
CREATE INDEX outbox_unpublished ON outbox (created_at) WHERE published_at IS NULL;
