-- See migrations/collection/000001_extensions.up.sql for the full rationale: CREATE EXTENSION
-- IF NOT EXISTS races on pg_extension when identity + collection migrate concurrently against
-- the shared dev database (and when Citus propagates the DDL to workers in prod). Swallow the
-- duplicate so the create is idempotent.
DO $$ BEGIN
  CREATE EXTENSION IF NOT EXISTS citext;
EXCEPTION WHEN duplicate_object OR unique_violation THEN NULL;
END $$;
