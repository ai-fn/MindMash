-- Citus distribution guard for sync tables.
-- On a plain Postgres instance this block is a no-op; on a Citus cluster it
-- distributes sync_applied_ops by user_id and co-locates it with note_types
-- (and the rest of the user_id-sharded tables) so idempotency lookups during
-- /sync/apply stay single-shard alongside the Collection writes they guard.
-- NOTE: once distributed, tables cannot be cleanly undistributed; the down
-- migration is intentionally a no-op.
DO $$
BEGIN
  IF EXISTS (SELECT 1 FROM pg_extension WHERE extname = 'citus') THEN
    PERFORM create_distributed_table('sync_applied_ops', 'user_id', colocate_with => 'note_types');
  END IF;
END $$;
