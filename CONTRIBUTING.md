# Contributing

Thanks for wanting to improve the trading engine. It is a small codebase with a
deliberate shape — three modules, each with its own conventions. Read this
before you start, and keep it updated when the conventions change.

## Repository layout

| Path | What it is | Module / package |
|---|---|---|
| `backend/` | Go server: matching engine, REST API, WebSocket hub, Postgres persistence | `trading-engine/backend` |
| `tui/` | Go + Bubble Tea terminal client (own module, talks to the backend) | `trading-engine/tui` |
| `sdk/` | Python agent SDK (`trading_engine` package), market-event definitions, examples, tests | `trading_engine` |
| `docs/` | Deep-dive architecture notes | — |

## Prerequisites

- **Go 1.27+** — for both `backend/` and `tui/` (each module pins its own version).
- **Python 3.9+** — for `sdk/` work.
- **Postgres** — `docker compose up -d` (or any Postgres; set `DATABASE_URL`).
- The engine does not require a database for its unit tests — only for running the server.

## First run

```bash
docker compose up -d          # Postgres
cp .env.example .env          # defaults match docker-compose

cd backend && go run .        # API + engine on :8080 (migrations auto-apply)
# in another terminal:
cd tui && go run .            # terminal UI — pick a metric, watch the floor
```

## Running the tests

Every module has its own test suite; the backend and TUI suites run **without**
a database.

```bash
# backend — engine + API
cd backend && go build ./... && go vet ./... && go test ./...

# tui — rendering + session integration (httptest + real WebSocket)
cd tui && go build ./... && go vet ./... && go test ./...

# sdk — event loader + validation
python -m venv sdk/.venv                       # once
sdk/.venv/bin/pip install -e "sdk[dev]"        # once
sdk/.venv/bin/python -m pytest sdk/tests -q
sdk/.venv/bin/python -m py_compile sdk/trading_engine/*.py sdk/examples/*.py
```

A change is not done until all three suites pass.

## Code conventions

### Backend (Go)

- **`engine.go` is a pure module**: no database, no I/O, no HTTP. All matching,
  welfare math, tournament state, and chat live here and are unit-testable
  without Postgres. `store.go` translates between that world and SQL.
- **Locking discipline.** Writers take `ex.lock()` (mutate, then
  `DrainPending()`), unlock, and only *then* touch the database. Readers take
  `rlock()`/`runlock()`. Never hold the engine lock across a DB call or a
  network write — that is the whole point of the background flusher.
- **Persistence goes through the `Pending` buffer.** Every mutation is queued
  into the pending batch and handed to the single background writer via
  `submitFlush(&pending)`. Handlers never write to Postgres directly. Chat is
  the deliberate exception: it is ephemeral and never enters `Pending`.
- **Money.** `float64` in the hot path, `NUMERIC(20,4)` at rest. Round prices to
  cents at entry (`roundCents`) and costs at settlement; float dust never
  reaches the ledger.
- **Errors.** HTTP errors are `{"error": "message"}` with `400/404/500` via
  `writeError`. Order placement failures use `PlaceError` + `placeErrorMessage`.
- **Adding an endpoint**: register the route in `server.routes()` (`api.go`),
  add a DTO, then a handler that either takes the write path (lock → mutate →
  drain → unlock → `submitFlush`) or the read path (`rlock`). If the response
  should appear on the live floor too, add the field to `LiveFrame` in
  `views.go` (the WS hub and `/api/snapshot` both read from it).
- **Adding a welfare metric**: implement the math in `engine.go`, register it
  in the `WelfareMetric` constants and `parseWelfareMetric`, and make sure
  `inequality()` returns an index in `[0, 1]` (that is what `Welfare.Gini`, the
  tape's `gini_after`, and the sim's solidarity trigger all consume). Update
  the docs.
- **Tests.** New engine behavior → `engine_test.go`; new endpoints → `api_test.go`
  (these use `httptest` and an in-memory exchange — no database).

### TUI (Go)

- Three layers, keep them separate:
  - `model.go` — bubbletea state machine: stages (metric picker → live floor),
    key handling, announce input.
  - `view.go` — pure rendering with lipgloss; no network calls in here.
  - `client.go` — the WS session: dial, subscribe, metric reseed, chat announce,
    reconnect. It streams messages into the program via `p.Send`.
- The session runs on its own goroutine; `Model.Update` only reacts to messages.
  Never block `Update` on network I/O.
- New keys: handle them in `Update`, document them in the footer help line and
  in the README's Terminal UI section.
- Tests: `view_test.go` renders synthetic frames; `client_test.go` runs the
  session against an `httptest` server that speaks real WebSocket.

### SDK (Python)

- 4-space indent, double-quoted strings, `from __future__ import annotations`.
- Keep `events.py` dependency-free (stdlib only): the event tests must run with
  only `pytest` installed.
- `TradingClient` is the REST surface; `WatchStream` is the WebSocket surface;
  strategies implement `on_tick(ctx)` and return `OrderIntent`s (or call
  `ctx.submit`). Public names are exported from `trading_engine/__init__.py`.
- Chat is best-effort from strategies: wrap `client.say(...)` in a try/except so
  a chat failure can never break trading.
- Adding a market event: write `sdk/events/<SLUG>.md` with a narrative and an
  embedded ```json definition (see `sdk/events/README.md`), then validate with
  `python sdk/examples/inspect_event.py sdk/events/<SLUG>.md`.

## Docs that must stay in sync

Behavior changes should update the relevant docs in the same commit:

- `README.md` — quickstart, API table, feature descriptions.
- `docs/ARCHITECTURE.md` — the deep-dive (sequence diagrams, lock/flush model).
- `sdk/events/README.md` — the event format reference.
- `sdk/README.md` — SDK usage.

## Commit conventions

- One logical change per commit; a concise imperative summary line that says
  *why* (`Add need-priority routing for solidarity orders`, not `update code`).
- Keep the working tree free of build artifacts (`tui/tui`, `__pycache__/`,
  `.venv/` are gitignored).
- Include the verification in the PR description: which suites were run.

## Pull request checklist

- [ ] `go build ./... && go vet ./... && go test ./...` green in `backend/`
- [ ] `go build ./... && go vet ./... && go test ./...` green in `tui/`
- [ ] `pytest sdk/tests` green (and `py_compile` clean)
- [ ] New endpoints/metrics/events documented (README + `docs/ARCHITECTURE.md` as applicable)
- [ ] No stray binaries, pyc files, or venvs in the diff
