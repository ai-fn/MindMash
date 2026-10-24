# ADR 0001: Modular monolith on one Citus database

Status: accepted (2026-10-24)

## Context

The services deploy as separate binaries, but every Postgres-backed service uses the same
`mindmesh` database. Their data is coupled in three ways:

- **Shared transactions.** A user's write and its sync ordering have to commit together.
  Collection, Scheduler and Media all bump the user's `user_usn` and write an `outbox` row in
  the same transaction as their own change.
- **Direct reads.** Scheduler reads and updates `cards` and reads `decks` and `deck_configs`.
  Sync builds its change feed straight from Collection's tables.
- **Ambiguous ownership.** Before this ADR, Collection and Media each created their own copy of
  `user_usn` and `outbox`. On Citus, the colocation group those tables joined depended on which
  service migrated first. Several stores also shared one `schema_migrations` history.

Giving each service its own database would turn every one of those transactions into a saga,
and would turn Sync's feed into an event-fed read model. At this stage that cost buys nothing.

## Decision

Keep one database and treat it as a modular monolith with explicit table ownership.

1. **One owner per table.** Only the owner's migrations create or alter a table. Tables
   that several services write live in the `platform` store (`migrations/platform`).
   `platform` is applied before any other store.
2. **One history per store.** Each store records its migrations in
   `schema_migrations_<store>`. `db.Migrate`, `task migrate` and the Helm job all add this
   table name.
3. **One colocation group.** Every per-user table is distributed by `user_id` and colocated
   with `user_usn`. That keeps each cross-service transaction on a single shard. Identity's
   tables are global and not distributed.
4. **Narrow writes into foreign tables.** A service writes another owner's table only as
   listed below. Any other change to Collection's entities goes through Collection's gRPC
   API. For example, Sync applies client mutations by calling Collection with the user's
   JWT.
5. **Each reader keeps its own column list.** A reader keeps the column list it needs next to
   its scan. A schema change to an owned table updates its listed readers in the same change.
6. **One relay for the whole database.** All services share one `outbox`, so exactly one relay
   is elected for the whole database, via an advisory lock. The `store` label on relay
   metrics only names which binary currently leads.

| Tables | Owner | Other writers | Other readers |
|---|---|---|---|
| `user_usn`, `outbox` | platform | collection, scheduler, media: `usn.Next`, `events.WriteOutbox` | sync (`usn.Current`), the relay |
| `note_types`, `notes`, `cards`, `decks`, `deck_configs`, `filtered_decks`, `tags`, `tombstones` | collection | scheduler: the scheduling columns of `cards` only (`state`, `queue`, `due`, `stability`, `difficulty`, `reps`, `lapses`, `last_review`, `usn`, `mtime`) | scheduler (`cards`, `decks`, `deck_configs`), sync (all of them) |
| `review_logs` | scheduler | none | none |
| `sync_applied_ops` | sync | none | none |
| `media`, `media_variants` | media | none | none |
| `users`, `profiles`, `refresh_tokens`, `email_verifications`, `oauth_identities` | identity | none | none |
| ClickHouse `review_events` | analytics | none | none |

## Consequences

- A user's write, its USN and its event commit atomically on one shard. No service needs a
  saga or dual-write.
- A schema change in Collection can break Scheduler or Sync at runtime. The table above
  is the checklist for such a change, and the integration tests run every reader against
  the real migrations.
- Readers depend on Collection's tables at runtime but not at migration time. Their own
  stores need only `platform`, so migration jobs can run in any order. Their queries, however,
  fail until Collection has migrated.
- Moving a service to its own database later requires two things:
  - replacing its direct reads with Collection RPCs or an event-fed read model;
  - giving the service its own USN source.
