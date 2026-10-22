-- Keyset delta pull scans tombstones by (user_id, usn); the PK (user_id, type, id) cannot serve it.
CREATE INDEX CONCURRENTLY IF NOT EXISTS tombstones_usn ON tombstones (user_id, usn);
