-- Lets the pending-upload GC find abandoned rows without scanning ready media.
CREATE INDEX CONCURRENTLY IF NOT EXISTS media_pending_mtime ON media (mtime) WHERE status = 'pending';
