-- Citus distribution guard.
-- On a plain Postgres instance this block is a no-op; on a Citus cluster it distributes
-- all media tables by user_id and co-locates them so intra-shard reads/writes resolve locally.
-- NOTE: once distributed, tables cannot be cleanly undistributed; the down migration is a no-op.
-- Idempotent: each table is distributed only if it isn't already (create_distributed_table
-- errors on an already-distributed table), so a partial re-run or the dev auto-heal path is safe.
DO $$
DECLARE
  child TEXT;
BEGIN
  IF NOT EXISTS (SELECT 1 FROM pg_extension WHERE extname = 'citus') THEN
    RETURN;  -- plain Postgres: nothing to distribute.
  END IF;
  -- media is the anchor; the rest colocate with it.
  IF NOT EXISTS (SELECT 1 FROM pg_dist_partition WHERE logicalrelid = 'media'::regclass) THEN
    PERFORM create_distributed_table('media', 'user_id');
  END IF;
  FOREACH child IN ARRAY ARRAY['media_variants','user_usn','outbox'] LOOP
    IF NOT EXISTS (SELECT 1 FROM pg_dist_partition WHERE logicalrelid = child::regclass) THEN
      PERFORM create_distributed_table(child, 'user_id', colocate_with => 'media');
    END IF;
  END LOOP;
END $$;
