-- Citus distribution guard.
-- On a plain Postgres instance this block is a no-op; on a Citus cluster it distributes
-- all media tables by user_id and co-locates them so intra-shard reads/writes resolve locally.
-- NOTE: once distributed, tables cannot be cleanly undistributed; the down migration is a no-op.
DO $$
BEGIN
  IF EXISTS (SELECT 1 FROM pg_extension WHERE extname = 'citus') THEN
    -- media is the anchor; the rest colocate with it.
    PERFORM create_distributed_table('media',          'user_id');
    PERFORM create_distributed_table('media_variants', 'user_id', colocate_with => 'media');
    PERFORM create_distributed_table('user_usn',       'user_id', colocate_with => 'media');
    PERFORM create_distributed_table('outbox',         'user_id', colocate_with => 'media');
  END IF;
END $$;
