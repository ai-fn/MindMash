-- CREATE EXTENSION IF NOT EXISTS is NOT concurrency-safe: its existence check and the
-- pg_extension insert are not atomic. In dev all Postgres services share one `mindmesh`
-- database and their golang-migrate jobs run concurrently (each holds a different advisory
-- lock because of the per-service x-migrations-table), so identity + collection both run
-- CREATE EXTENSION citext at the same instant and one loses the pg_extension_name_index race
-- (SQLSTATE 23505). The same duplicate-key can also surface in prod when Citus propagates the
-- DDL to worker nodes. Swallow the race so each create is idempotent.
DO $$ BEGIN
  CREATE EXTENSION IF NOT EXISTS ltree;
EXCEPTION WHEN duplicate_object OR unique_violation THEN NULL;
END $$;

DO $$ BEGIN
  CREATE EXTENSION IF NOT EXISTS citext;
EXCEPTION WHEN duplicate_object OR unique_violation THEN NULL;
END $$;
