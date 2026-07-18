-- Citus distribution guard for sync tables.
-- On a plain Postgres instance this block is a no-op; on a Citus cluster it
-- distributes sync_applied_ops by user_id and co-locates it with note_types
-- (and the rest of the user_id-sharded tables) so idempotency lookups during
-- /sync/apply stay single-shard alongside the Collection writes they guard.
-- NOTE: once distributed, tables cannot be cleanly undistributed; the down
-- migration is intentionally a no-op.
-- Idempotent: distribute only if not already distributed (create_distributed_table errors
-- on an already-distributed table), so a re-run or the dev auto-heal path is safe.
-- Guard-then-return: on plain Postgres the pg_dist_partition catalog does not exist, and a
-- single IF that references it (even behind an AND) fails at plan time. Return early before
-- any pg_dist_partition reference so the block is a true no-op without Citus.
DO $$
BEGIN
  IF NOT EXISTS (SELECT 1 FROM pg_extension WHERE extname = 'citus') THEN
    RETURN;  -- plain Postgres: nothing to distribute.
  END IF;
  IF NOT EXISTS (SELECT 1 FROM pg_dist_partition WHERE logicalrelid = 'sync_applied_ops'::regclass) THEN
    PERFORM create_distributed_table('sync_applied_ops', 'user_id', colocate_with => 'note_types');
  END IF;
END $$;
