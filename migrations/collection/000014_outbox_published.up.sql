-- Lets the relay purge published rows without scanning the outbox.
CREATE INDEX CONCURRENTLY IF NOT EXISTS outbox_published ON outbox (published_at) WHERE published_at IS NOT NULL;
