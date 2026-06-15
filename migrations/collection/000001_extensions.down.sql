-- NOTE: citext is intentionally omitted here because the identity store also depends on it
-- (column users.email).  Dropping it here would cascade into identity tables.
-- ltree is collection-only and can be safely dropped.
DROP EXTENSION IF EXISTS ltree;
