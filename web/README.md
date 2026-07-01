# MindMesh Web SPA

The MindMesh web client: a React + TypeScript SPA built with Vite. This is the **Phase-1
study-loop MVP** — login, deck picker, the core study loop (review queue → reveal → grade), and
minimal authoring to reach a studyable state. The full editor/browser/stats/plugins/PWA surface
from [`docs/detailed/12-frontend-web-spa.md`](../docs/detailed/12-frontend-web-spa.md) is deferred
to Phase 2.

## Prerequisites

- Node 22 (see `.nvmrc`; `nvm use` if you use nvm)
- `npm install`

## Local development

The backend is **not** behind a single gateway — each service exposes its own HTTP port. The Vite
dev server proxies API requests to the right service by path, so the SPA always talks to
`localhost:5173` and never needs CORS configured on the Go side.

Default port convention (override an individual target via the matching `VITE_*_URL` env var):

| Service    | Default URL             | Env override          | Example routes                                                     |
| ---------- | ----------------------- | --------------------- | ------------------------------------------------------------------ |
| Identity   | `http://localhost:8080` | `VITE_IDENTITY_URL`   | `/v1/auth/*`, `/v1/users/*`, `/v1/profiles/*`                      |
| Collection | `http://localhost:8081` | `VITE_COLLECTION_URL` | `/v1/decks/*`, `/v1/cards/{id}`, `/v1/notes/*`, `/v1/note-types/*` |
| Scheduler  | `http://localhost:8082` | `VITE_SCHEDULER_URL`  | `/v1/study/*`, `/v1/cards/{id}/answer`, `/v1/cards/{id}/preview`   |
| Sync       | `http://localhost:8083` | `VITE_SYNC_URL`       | `/v1/sync/*`                                                       |
| Analytics  | `http://localhost:8084` | `VITE_ANALYTICS_URL`  | `/v1/stats/*` (overview, heatmap, retention, breakdown)            |
| Search     | `http://localhost:8085` | `VITE_SEARCH_URL`     | `/v1/search`                                                       |

Because the same Go binary defaults `HTTP_ADDR` to `:8080`, running all six services
simultaneously locally requires overriding each one's address, e.g.:

```bash
HTTP_ADDR=:8080 go run ./cmd/identity
HTTP_ADDR=:8081 go run ./cmd/collection
HTTP_ADDR=:8082 go run ./cmd/scheduler
HTTP_ADDR=:8083 go run ./cmd/sync
HTTP_ADDR=:8084 go run ./cmd/analytics
HTTP_ADDR=:8085 go run ./cmd/search
```

The Stats (analytics) and Browse (search) screens need the last two services running; without
them the Vite proxy has nothing to forward `/v1/stats` and `/v1/search` to. Auth, decks, and the
study loop only need Identity, Collection, and Scheduler.

Then, from `web/`:

```bash
npm run dev
```

and open `http://localhost:5173`.

Path-overlap note: `GET /v1/cards/{id}` is owned by Collection, but
`POST /v1/cards/{id}/answer` and `GET /v1/cards/{id}/preview` are owned by Scheduler. The dev
proxy in `vite.config.ts` orders its regex routes so the Scheduler sub-routes (and `/v1/study`)
match _before_ the generic `/v1/cards` → Collection rule.

## Scripts

| Script                 | Description                                    |
| ---------------------- | ---------------------------------------------- |
| `npm run dev`          | Start the Vite dev server (with backend proxy) |
| `npm run build`        | Type-check + production build                  |
| `npm run preview`      | Preview the production build locally           |
| `npm run typecheck`    | `tsc -b` project-references type-check         |
| `npm run lint`         | ESLint (flat config)                           |
| `npm run format`       | Prettier — write                               |
| `npm run format:check` | Prettier — check only                          |
| `npm run test`         | Vitest unit/component tests (run once)         |
| `npm run test:watch`   | Vitest in watch mode                           |
| `npm run coverage`     | Vitest with coverage report                    |
| `npm run e2e`          | Playwright end-to-end tests                    |

## End-to-end tests (Playwright)

The specs in `web/e2e` drive the redesigned happy path against a real running backend (no mocks),
sharing the register/login/seed helpers in `web/e2e/helpers.ts`:

- `study-loop.spec.ts` — login, create a deck, quick-add a Basic note, study it, reveal, grade
  with the default 2-button "Know / Don't Know" UI, and reach the deck-complete state.
- `browse-stats-settings.spec.ts` — the Stats and Browse screens reach their analytics/search
  backends (asserted via a 2xx on `/v1/stats/overview` and `/v1/search`) and settle into their
  dashboard/results-or-empty states; and Settings applies a theme change (`<html data-theme>`)
  and flips "advanced grading" on, verifying the study screen then shows the 4-button
  Again/Hard/Good/Easy UI.

1. Start all six backend services first, exactly as in the
   [Local development](#local-development) port table above (Identity, Collection, Scheduler,
   Sync, Analytics, Search). The Stats and Browse assertions need Analytics and Search; the
   study-loop spec only exercises Identity, Collection, and Scheduler.
2. From `web/`, run:

   ```bash
   npm run e2e
   ```

   Playwright's `webServer` config starts (or reuses, if one is already running) the Vite dev
   server for you — there's no separate `npm run dev` step needed.

Each spec registers a fresh throwaway user per run directly via the `POST /v1/auth/register` API
(there's no UI registration screen in this MVP slice), then drives everything else through the
UI starting at `/login`. The Stats/Browse assertions are deliberately tolerant of empty data —
the analytics event pipeline and search index are asynchronous, so a just-created account may
have no stats/results yet; the tests assert the integration is reachable and the screen settles,
not specific row counts.
