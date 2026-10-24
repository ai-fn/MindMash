-- No-op on plain Postgres. user_usn anchors the colocation group every per-user table joins,
-- so a user's rows and counter share a shard whichever service writes them.
DO $$
BEGIN
  IF NOT EXISTS (SELECT 1 FROM pg_extension WHERE extname = 'citus') THEN
    RETURN;
  END IF;
  IF NOT EXISTS (SELECT 1 FROM pg_dist_partition WHERE logicalrelid = 'user_usn'::regclass) THEN
    PERFORM create_distributed_table('user_usn', 'user_id');
  END IF;
  IF NOT EXISTS (SELECT 1 FROM pg_dist_partition WHERE logicalrelid = 'outbox'::regclass) THEN
    PERFORM create_distributed_table('outbox', 'user_id', colocate_with => 'user_usn');
  END IF;
END $$;
