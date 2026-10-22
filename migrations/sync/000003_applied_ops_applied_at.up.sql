-- Lets the retention purge find expired op_ids without scanning every shard.
CREATE INDEX CONCURRENTLY IF NOT EXISTS sync_applied_ops_applied_at ON sync_applied_ops (applied_at);
