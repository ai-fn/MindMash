# ADR 0002: Data is scoped per user; profiles do not partition it yet

Status: accepted (2026-10-24)

## Context

The requirements ask for multiple profiles with separate datasets (functional-requirements
§12.3). Identity already models profiles: `ActivateProfile` mints a token carrying
`profile_id`, and rows record the profile they were created under.

Reads, however, used two different scopes:

- Collection, Scheduler, Sync and Media filter by `user_id` only.
- Search and Analytics filtered by `user_id` and `profile_id`.

After a profile switch, Collection therefore listed a card while Search could not find it and
the stats left out its reviews.

## Decision

Until profiles get full isolation, every read is scoped by `user_id` alone.

- **Stored but not filtered.** `profile_id` is still written when a row is created (derived
  rows such as cards inherit it from their note), but no query filters on it.
- **Search and Analytics follow the user.** They no longer filter on `profile_id`.
- **ClickHouse sort key.** ClickHouse orders `review_events` by `(user_id, ts, review_id)`,
  so time ranges prune within a user.

## Consequences

- Every service shows the same data whichever profile is active. Profiles are effectively
  labels.
- §12.3 is not met yet. Full isolation is a cross-cutting change, not a filter flag:
  - every Postgres query and index gains `profile_id`;
  - the USN counter and the sync cursor become per profile;
  - deck configs and filtered decks move under a profile;
  - Search and Analytics restore the `profile_id` term and sort key.
- Every row already carries the `profile_id` it was created under, so isolation needs no
  backfill. Rows that several profiles touch, such as shared tags, would need a rule
  for which profile owns them.
