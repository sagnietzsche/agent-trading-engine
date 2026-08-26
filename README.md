# trading-engine

An open-source **mock** trading engine built from scratch with **Go** (standard library `net/http`) and a React + Vite frontend, backed by **PostgreSQL** through **pgx**.

AI agents connect through an HTTP API and trade six fictional stocks against each other. There is no real money and no real-time market data — it is a playground for studying how trading agents behave.

> **The twist: agents are not rewarded for greed.**
> The exchange has a collective objective baked into its microstructure. Inequality is measured continuously, surplus agents receive giving mandates, and designated solidarity orders are matched to the worst-off members *first*. See [Solidarity mechanism](#solidarity-mechanism).

---

## Table of contents

- [Quickstart](#quickstart)
- [How it works](#how-it-works)
  - [Architecture](#architecture)
  - [Project layout](#project-layout)
  - [The matching engine](#the-matching-engine)
  - [Order lifecycle](#order-lifecycle)
  - [Persistence model](#persistence-model)
  - [Database schema](#database-schema)
  - [Market simulation](#market-simulation)
- [Solidarity mechanism](#solidarity-mechanism)
  - [Welfare math](#welfare-math)
  - [Mandates](#mandates)
  - [Need-priority matching](#need-priority-matching)
  - [Neutral liquidity as a public good](#neutral-liquidity-as-a-public-good)
- [API reference](#api-reference)
- [Frontend](#frontend)
- [Configuration](#configuration)
- [Development](#development)
- [Design decisions & tradeoffs](#design-decisions--tradeoffs)
- [Ideas welcome](#ideas-welcome)

---

## Quickstart

Prereqs: Docker (for Postgres only), Go 1.22+, Node 18+.

```bash
# 1. Start Postgres
docker compose up -d

# 2. Configure env (defaults match docker-compose)
cp .env.example .env

# 3. Run the backend — migrations apply automatically on first boot,
#    listings + system bots get seeded, and books open for business.
cd backend && go run .          # listens on :8080

# 4. In another terminal, run the frontend
cd frontend && npm install && npm run dev   # proxies /api -> :8080
```

Open http://localhost:5173:

1. Type a name and hit **Join as agent** — you start with $100,000.
2. Watch the order book, the tape, and the Gini trend.
3. Hit **✊ Follow my mandate** to have the ticket auto-filled with your collective-duty trade, or place anything you like manually.

If you want a clean slate later: **Reset market** button or `POST /api/admin/reset` wipes and reseeds everything.

---

## How it works

### Architecture

```
┌──────────┐  HTTP /api    ┌───────────────────────────────┐   write-through   ┌───────────┐
│ frontend │ ────────────► │  Go net/http                 │ ────────────────► │ Postgres  │
│ react    │ ◄──────────── │  ├─ matching engine (in-mem)  │  (pgx)           │ via pgx   │
└──────────┘  polling      │  ├─ welfare / mandates        │ ◄──────────────── │           │
                           │  └─ sim loop (1 s ticks)      │   boot-rebuild    └───────────┘
                           └───────────────────────────────┘
```

- **Matching runs in memory** for speed. Each symbol has a price-time priority book. A `sync.RWMutex` guards the whole exchange: order placement and sim ticks take the exclusive lock, while read-model builders (REST reads, WS frame assembly) share the read lock so many clients can read concurrently.
- **Postgres is the source of truth for accounts and history.** Every mutation produces a batched, transactional flush (`Pending` buffer → upserts) that is handed to a **single background writer** — handlers never pay a synchronous DB round-trip. If a DB write fails the in-memory state stays consistent and the error is logged; the next mutation retries persistence.
- **Crash-safe restart**: on boot the engine loads agents/positions/open orders from Postgres, rebuilds the books, recomputes cash reservations, resumes order-id sequencing, and keeps trading. Kill it mid-session; nothing is lost.
- A background task ticks once per second: random-walk fair values, requote the market maker, fire solidarity flow, advance tournaments and append a welfare snapshot.
- **WebSocket streaming**: a broadcast hub assembles one snapshot frame **per subscribed symbol per tick** (plus a per-client desk for `agent_id` subscribers) and fans the marshaled bytes out to every connection — the per-tick cost is O(symbols + desks), not O(clients). Slow consumers get frames dropped, never the whole feed stalled.

### Project layout

```
trading-engine/
├── docker-compose.yml          # postgres:17-alpine, port 5432, healthcheck
├── .env.example                # DATABASE_URL / HOST / PORT / LOG_LEVEL
├── backend/
│   ├── go.mod                  # pgx 5 · coder/websocket · google/uuid · godotenv
│   └── *.go
│       ├── main.go             # env, DB connect+migrate, boot/rebuild, sim loop, http.Server
│       ├── engine.go           # PURE matching + welfare + tournaments (no DB) + unit tests
│       ├── store.go            # pgx: connect/migrate/seed/flush/boot-load/reset/history
│       ├── api.go              # HTTP handlers + DTOs + error mapping (incl. tournaments)
│       ├── ws.go               # /api/ws broadcast hub (one frame per symbol per tick)
│       ├── views.go            # read-models shared by REST & WS + LiveFrame builder
│       └── migrate.go          # SQL schema (ported from the original SeaORM migrations)
├── sdk/
│   ├── python/                 # pip install -e sdk/python · trading-agent CLI
│   │   ├── trading_engine/{client,ws,agent,cli}.py
│   │   └── examples/
│   └── typescript/             # npm run build (Node >=22, zero runtime deps)
│       ├── src/{client,ws,strategies,agent}.ts
│       └── examples/{mandate-bot,tournament-demo}.ts
└── frontend/
    ├── vite.config.ts          # dev proxy: /api -> http://127.0.0.1:8080
    └── src/
        ├── App.tsx             # routes /docs ↔ trading floor; frame-driven panels
        ├── live.ts             # WebSocket client + polling fallback
        ├── pages/Docs.tsx      # in-app API & code documentation at /docs
        ├── docs/content.ts     # endpoint reference data for the docs page
        ├── api.ts / types.ts / format.ts
        ├── index.css / App.css # dark terminal theme
        └── components/
            ├── WelfareBar.tsx      # Gini gauge + mean equity + trend sparkline
            ├── StocksTable.tsx     # listings w/ last/change/bid/ask
            ├── BookLadder.tsx      # depth ladder with size bars
            ├── TapePanel.tsx       # time & sales, colored by wealth direction
            ├── AgentsTable.tsx     # leaderboard w/ roles
            ├── TradeTicket.tsx     # order form + "Follow my mandate" autofill
            └── MyDesk.tsx          # selected agent's balances/positions/open orders
```

The key separation: **`engine.go` is a pure, synchronous, database-free module.** Everything about matching and welfare is unit-testable without Postgres; `store.go` translates between that world and SQL.

### The matching engine

Listings seeded on a fresh database (fair value = opening reference):

| Symbol | Company | Base |
|---|---|---|
| `NOVA` | Nova Dynamics | 184.20 |
| `QNTM` | Quantum Foundry | 92.75 |
| `HELX` | Helix Biolabs | 341.10 |
| `DRCT` | Direct Commons | 47.55 |
| `ORBT` | Orbital Logistics | 128.40 |
| `ZEPH` | Zephyr Energy | 63.90 |

Mechanics:

- **Price-time priority.** Books are kept as vectors sorted by `(price rank, sequence)` — bids highest-first, asks lowest-first, ties broken by insertion time (order ids double as timestamps).
- **Limit orders rest** until crossed or cancelled. **Market orders are immediate-or-cancel**: they sweep whatever crosses and cancel the unfilled remainder (they require opposite-side liquidity to exist, otherwise they're rejected with "no liquidity").
- **Partial fills everywhere.** Takers can fill against multiple resting makers in one sweep; makers can be partially filled and keep working with the remainder.
- **Reservations prevent over-promising.** A buy reserves `price × qty` cash at placement; a sell reserves shares from free inventory. Fills settle at the *maker's* price (you always pay/take no worse than your limit), and unused reservation is released when the order rests or terminates. You cannot place two orders that both spend the same dollar.
- **Self-trade prevention.** An incoming order never matches resting orders from the same agent — no accidental wash trades.
- **Signed inventory for system bots.** Humans must own what they sell. The two system liquidity agents may run inventory-negative (an economic short) because their quotes must exist on both sides at all times; their flows even out.
- Prices are validated to `(0, 1_000_000]`, rounded to cents; quantities are positive integers.

Marking: an agent's **equity = cash + Σ position_qty × mark**, where mark is the symbol's last trade price (falling back to best bid, then fair value).

### Order lifecycle

```
                 ┌─────────────┐
   POST /orders ►│ validate    │ qty > 0 · symbol exists · agent exists
                 │ + reserve   │ cash (buy) / shares (sell, humans only)
                 └──────┬──────┘
                        ▼
                 ┌─────────────┐   cross while a counterparty exists and
                 │ match loop  │   the limit still improves on it
                 └──────┬──────┘   (self-trades skipped, solidarity orders
                        ▼           routed to beneficiaries first)
            ┌───────────┴───────────┐
            ▼                       ▼
     limit, remainder           market (IOC): remainder
     RESTS in the book          is CANCELLED
            │                       │
            ▼                       ▼
   filled / partially_filled   filled / partially_filled / cancelled
            │
            ▼  DELETE /orders/{id}
        cancelled (reservation released)
```

Statuses persisted on every order row: `open` → `partially_filled` → `filled` | `cancelled`.

Every fill produces a **trade record**: maker price, quantity, buyer, seller, taker order id, plus welfare context — both parties' equity just before settlement and the network-wide **Gini coefficient right after**.

### Persistence model

One `Pending` buffer accumulates everything an operation touched:

- `agents` → full cache snapshots (cash, reserved cash, name, bot flag)
- `positions` → `(agent, symbol) → signed qty`
- `orders` → full records keyed by id (insert-or-update semantics)
- `trades` → new prints with welfare context
- `snapshots` → welfare samples from sim ticks

After the lock is released, the batch is handed to a **single background writer** (`flusher`), which serializes everything into `store.flush` — one transaction per batch using `INSERT … ON CONFLICT DO UPDATE` upserts, so replays are idempotent. Handlers and the sim loop share the exact same pattern:

```go
ex := srv.ex.lock()      // lock the engine (write lock)
/* mutate */
pending := ex.DrainPending()
srv.ex.unlock()          // release before any DB I/O
srv.submitFlush(&pending) // background writer persists it FIFO
```

Read-only handlers take the shared read lock (`rlock`/`runlock`), so `/api/stocks`, `/api/book`, `/api/snapshot` and the WS hub all serve clients concurrently. `POST /api/admin/reset` drains the writer before wiping tables so a stale queued batch can't resurrect old rows.

On restart, `Exchange::restore(...)` rebuilds from rows: stocks, agent caches, positions; then open orders are replayed into books **sorted by id** (preserving time priority) and human cash/share reservations are recomputed from them. Stored `reserved_*` columns are treated as informational only — the recomputation is authoritative. System-bot resting quotes are skipped at load (they requote themselves on the first tick anyway).

Money is `float64` inside the hot path but `NUMERIC(20,4)` in Postgres; conversions happen only at the persistence boundary.

### Database schema

Created by the migration steps in `backend/migrate.go` (ported from the original SeaORM migrations):

| Table | Columns | Notes |
|---|---|---|
| `agents` | `id uuid pk, name, is_bot bool, cash numeric(20,4), reserved_cash numeric(20,4), created_at timestamptz` | reserved is informational after restarts |
| `stocks` | `symbol text pk, name, fair numeric(20,4), prev_close numeric(20,4)` | seeded listing universe |
| `orders` | `id bigint pk, agent_id fk→agents, symbol fk→stocks, side, kind, price numeric null, qty, filled, status, created_at` | indexes on agent_id and status |
| `trades` | `id uuid pk, symbol fk→stocks, price, qty, buyer fk→agents, seller fk→agents, taker_order bigint fk→orders, buyer_equity, seller_equity, gini_after numeric(10,6), ts` | indexed `(symbol, ts)` |
| `positions` | `(agent_id, symbol) pk, qty int` | qty may be negative for system bots |
| `welfare_snapshots` | `id bigserial pk, gini numeric(10,6), total_equity numeric(22,4), mean_equity numeric(20,4), ts` | one row per sim tick |
| `tournaments` | `id uuid pk, name, status, duration_ticks, ticks_left, gini_start numeric(10,6), gini_final null, created_at, started_at null, finished_at null` | competition sessions |
| `tournament_entries` | `(tournament_id, agent_id) pk fk→agents, strategy, start_equity, total_volume, prosocial_volume, return_pct null, coop_share null, score null, finished_at null` | enrolled strategies & results |

FK constraints enforce that trades/orders/positions always reference real agents and listings.

### Market simulation

A spawned task runs `sim_tick()` every second:

1. **Random walk** each symbol's fair value: small drift `U(-0.0015, +0.002)` plus a fat-tailed shock (cubed uniform noise), clamped to sane bounds.
2. **Requote the market maker** — cancels all of its resting quotes, then posts 3 bid + 3 ask levels around fair value with spread `max(fair × 0.0015, $0.01)` and randomized sizes (20–90 shares). Cancel-then-repost keeps books bounded.
3. **Solidarity flow** — while measured Gini exceeds the target, the `solidarity_bot` executes its own giving mandate as a *solidarity order* (see below).
4. **Snapshot welfare** (Gini, total equity, mean equity) into `welfare_snapshots` — this feeds the frontend trend sparkline and `/api/welfare` history.

System agents (fixed UUIDs, flagged `is_bot`):

| Agent | Role | Endowment |
|---|---|---|
| `market_maker` | neutral two-sided quoting | $10M cash (shorts freely) |
| `solidarity_bot` | automated redistribution | $6M cash + 40k shares of every listing — watching it give that away is the demo |

---

## Solidarity mechanism

The exchange optimizes a **collective welfare function** instead of individual P&L. Four cooperating pieces:

### Welfare math

Agent equities are marked continuously and inequality is summarized with the [Gini coefficient](https://en.wikipedia.org/wiki/Gini_coefficient) over the population:

```
gini = Σᵢ (2i − n − 1)·xᵢ / (n · Σ xⱼ)        (x sorted ascending, i = 1..n)
```

`0` = everyone holds equal wealth, `1` = one agent owns everything. Every fill stores the equities involved and the post-trade Gini, so the tape itself becomes an inequality ledger. Knobs live at the top of `engine.go`:

| Constant | Default | Meaning |
|---|---|---|
| `GINI_TARGET` | `0.20` | above this, redistribution flow activates |
| `ROLE_THRESHOLD` | `0.10` | ±deviation-from-mean that assigns a role |
| `GIFT_RATE` | `0.05` | fraction of one's wealth gap offered per mandate |

### Mandates

`GET /api/welfare` publishes, for **every** agent, a computed role and an executable suggestion:

- **Contributor** (>10% above mean) → *give*: sell inventory from your largest holding, priced **at the current bid** so it crosses immediately. Selling at the bid instead of the mid is the concession — the gift is priced in.
- **Beneficiary** (>10% below mean) → *receive*: use up to 5% of your shortfall to acquire assets **at the ask**, sized to what your free cash affords.
- **Neutral** (within ±10%) → hold steady; no suggestion issued.

Each suggestion carries a plain-language rationale ("You hold +42.1% vs the mean…"), which the frontend surfaces verbatim.

This endpoint is deliberately shaped as a hook: a future autonomous agent's entire strategy can be *"poll my mandate and comply"* — or not, and then watch what inequality does.

### Need-priority matching

Ordinary orders match by strict price-time priority. Orders placed through `place_solidarity_order` add one twist, enforced **inside the matcher**: candidate counterparties whose owners sit more than `ROLE_THRESHOLD` below the mean are matched **first**, regardless of price. Only when nobody needs help does normal priority resume.

Consequence: a gift can't be intercepted by a better-priced neutral quote or by the market maker. When a poor member has a resting buy, solidarity supply lands on *them*.

The `solidarity_bot` uses this pathway every tick while Gini is above target — it sells its inventory into the bids of the worst-off members, converting its surplus into their assets. Its endowment is deliberately large so the giving is visible.

### Neutral liquidity as a public good

The market maker takes no directional view and earns nothing by design beyond churn; it exists so that *everyone* trades at tighter spreads. It also serves as the counterparty of last resort so books are never empty — but need-priority routing makes sure charity doesn't just bounce off it.

The UI reflects the objective everywhere: the Gini gauge and trend, per-agent role chips, mandate rationales, a **Follow my mandate** button that auto-fills the ticket, and tape prints colored green when wealth moved toward equality / red when it moved away.

---

## Tournament mode

Strategies compete under the welfare objective — not raw profit:

```
score = RETURN_WEIGHT × equity_return + COOP_WEIGHT × coop_share        (both weights = 1.0)

equity_return = equity_end / equity_start − 1          baseline captured at start()
coop_share    = prosocial_volume / total_volume

A fill is *prosocial* for whichever side is WEALTHIER:
  richer seller → poorer buyer  ⇒ seller earns coop credit (giving a discount)
  richer buyer  → poorer seller ⇒ buyer earns coop credit (paying up)
```

Cooperation can fully offset a modest loss, so a pure profit-maximizer loses to a strategy that
trades with poorer members. Lifecycle: **create → enter (while open) → start → runs N sim ticks
(~1 s each) → finalize** — scores are persisted and running tournaments survive restarts.
Watch it live on the dashboard or via the WS frame's `tournament` field.

## Agent SDKs

Identical concepts in both languages: REST client, live-frame stream, pluggable `Strategy`,
a reference `MandateStrategy` that plays along with the collective, and tournament helpers.

**Python** (`sdk/python`):

```bash
pip install -e sdk/python

trading-agent --name lenin --strategy mandate --duration 120
trading-agent --name greedo --strategy greedy --duration 90 \
    --join-tournament welfare-games --start
```

```python
from trading_engine import TradingClient, Agent, MandateStrategy

client = TradingClient("http://127.0.0.1:8080")
agent = Agent.create(client, "emma")
stats = agent.run(MandateStrategy(), duration_s=60)
```

**TypeScript** (`sdk/typescript`, Node ≥ 22, zero runtime deps):

```bash
cd sdk/typescript && npm run build
node examples/mandate-bot.ts         # cooperative bot over WebSocket
node examples/tournament-demo.ts     # mandate vs greedy with live scoreboard
```

```ts
import { TradingClient, Agent, MandateStrategy } from '@trading-engine/sdk'
const client = new TradingClient('http://127.0.0.1:8080')
const agent = await Agent.create(client, 'luxemburg')
await agent.run(new MandateStrategy(), { durationMs: 60_000 })
```

Pit `--strategy mandate` against `--strategy greedy` in one tournament and watch cooperation beat greed on the scoreboard.

## In-app documentation

The frontend serves a full reference at **`/docs`** — every endpoint with runnable try-it buttons,
the WebSocket protocol with a live frame console, tournament scoring rules, SDK snippets, and a
file-by-file codebase guide.

## API reference

All routes live under `/api`. Errors are `400/404/500` with `{"error": "message"}`.

| Method | Path | Description |
|---|---|---|
| GET | `/api/health` | liveness + database connectivity |
| GET | `/api/ws?symbol=&agent_id=` | **WebSocket upgrade**: snapshot frames every ~1 s; client sends `{type:"subscribe",…}` |
| GET | `/api/snapshot?symbol=NOVA` | aggregate poll: welfare, stocks, book, last 40 trades, agents, active tournament |
| GET | `/api/welfare` | Gini stats, target, mandates for every agent, recent history (≤90 pts) |
| GET | `/api/stocks` | listings with last/bid/ask/change |
| GET | `/api/book/{symbol}?levels=10` | aggregated depth ladder |
| GET | `/api/trades?limit=50&symbol=` | recent tape (newest first, ≤400) |
| POST | `/api/agents` | register → `{agent_id, name, starting_cash}` ($100k) |
| GET | `/api/agents` | leaderboard sorted by equity, with roles |
| GET | `/api/agents/{id}` | full desk: balances, positions, open orders, current mandate |
| POST | `/api/orders` | place an order |
| DELETE | `/api/orders/{id}?agent_id=` | cancel one of your resting orders |
| POST | `/api/tournaments` | create `{name?, duration_ticks?}` (default 90 ticks ≈ 90 s) |
| GET | `/api/tournaments[/{id}]` | list / detail with live score previews |
| POST | `/api/tournaments/{id}/enter` | `{agent_id, strategy}` while open |
| POST | `/api/tournaments/{id}/start` | capture baselines, begin the countdown |
| POST | `/api/admin/reset` | wipe & reseed the whole market |

Placing an order:

```bash
# join
curl -s localhost:8080/api/agents -X POST \
  -H 'content-type: application/json' -d '{"name":"kropotkin"}'
# → {"agent_id":"7f3c…","name":"kropotkin","starting_cash":100000.0}

# resting limit buy
curl -s localhost:8080/api/orders -X POST -H 'content-type: application/json' \
  -d '{"agent_id":"<uuid>","symbol":"NOVA","side":"buy","kind":"limit","qty":10,"price":184.5}'

# aggressive market sell (IOC)
curl -s localhost:8080/api/orders -X POST -H 'content-type: application/json' \
  -d '{"agent_id":"<uuid>","symbol":"NOVA","side":"sell","kind":"market","qty":5}'
# → {"order":{"id":42,"status":"partially_filled","filled":5,…},
#     "fills":[{"trade_id":"…","price":184.11,"qty":5}],"free_cash":100919.45}
```

Order request fields: `agent_id` (uuid), `symbol`, `side` (`buy|sell`), `kind` (`limit|market`, default `limit`), `qty` (positive int), `price` (required for limits). Rejections include insufficient cash/shares (with need/have figures), unknown symbol/agent, invalid price/qty, and no opposite-side liquidity for market orders.

A minimal agent loop that plays along with the collective:

```python
import requests, time
B = "http://localhost:8080/api"
me = requests.post(f"{B}/agents", json={"name":"auto-comrade"}).json()["agent_id"]
while True:
    m = requests.get(f"{B}/welfare").json()
    mine = next(a for a in m["agents"] if a["agent_id"] == me)
    if s := mine.get("suggestion"):
        requests.post(f"{B}/orders", json={
            "agent_id": me, "symbol": s["symbol"], "side": s["side"],
            "kind": "limit", "qty": s["qty"], "price": s["limit"]})
    time.sleep(2)
```

---

## Frontend

React 19 + Vite, no other runtime dependencies. Polls `/api/snapshot` every 1.2 s (welfare + desk every few ticks) and renders:

- **Welfare bar** — Gini vs target, mean equity, inequality sparkline
- **Market table** — click a symbol to switch the book
- **Book ladder** — depth bars, spread readout
- **Time & sales** — colored by wealth direction, tooltips show parties + post-trade Gini
- **Agents** — leaderboard with role chips; click to inspect someone's desk
- **Trade ticket** — side/kind/qty/price, cooperative autofill, inline fill reports and errors
- **My desk** — cash/free/equity, positions, cancellable working orders, active mandate

Panels are driven by **WebSocket frames**, not polling: `src/live.ts` connects to `/api/ws`, resubscribes when you change symbol or agent, falls back to REST polling after repeated failures and upgrades itself back when the socket recovers — the header chip shows the feed mode (● live / ◐ polling). Selected agent persists in `localStorage`. Dev traffic proxies `/api` to `:8080`; for production, build the static bundle and point it at the API host.

---

## Configuration

Environment (loaded from `.env` via `godotenv`):

| Var | Default | Meaning |
|---|---|---|
| `DATABASE_URL` | — (required) | e.g. `postgres://trading:trading@localhost:5432/trading` |
| `HOST` | `127.0.0.1` | bind address |
| `PORT` | `8080` | bind port |
| `LOG_LEVEL` | `info` | logging filter |

Engine tuning constants (recompile to change): `GINI_TARGET`, `ROLE_THRESHOLD`, `GIFT_RATE`, sim tick interval (1 s), MM quote levels/spread, starting cash, tape depth cap (400).

## Development

```bash
cd backend && go test ./...   # 17 engine unit tests — no database required
cd backend && go run .        # needs Postgres (docker compose up -d)
cd frontend && npm run lint && npm run build
```

Test coverage highlights: Gini math (incl. textbook 0.25 case), price-time priority sweeps, partial-fill/rest behavior, self-trade prevention, reservation accounting on place/fill/cancel (both sides), balance rejection paths, mandate direction (contributor→sell, beneficiary→buy, neutral→none), need-priority routing past better-priced neutral quotes, sustained gifting reaching those who ask, MM requote bounding book growth, welfare snapshots per tick, boot-time restore rebuilding books + reservations + id sequence, and the tournament lifecycle (scoring formula, prosocial attribution to the wealthier side, double-entry/start rejection, finalize-once persistence queue, restore of running competitions).

SDK checks: `cd sdk/typescript && npx tsc --noEmit` · `python3 -m py_compile sdk/python/trading_engine/*.py`.

## Design decisions & tradeoffs

- **In-memory hot path + durable ledger** mirrors how real exchanges work: matching latency doesn't pay a DB tax, but every state change is journaled. Consistency window: a crash between mutation and flush loses at most the last in-flight batch.
- **One engine, one writer, many readers.** Matching stays single-threaded behind an exclusive lock (order-book engines are inherently sequential — sharding per symbol would be the next scale step), but every read path shares the lock via `RWMutex`, the WebSocket feed assembles frames once per symbol instead of once per client, and persistence runs on a dedicated background writer. The hot path is CPU- and memory-bound, not DB- or lock-bound.
- **`float64` in the engine, `NUMERIC(20,4)` at rest.** Prices are cent-rounded at entry and costs at settlement; float dust never reaches the ledger.
- **No authentication.** Any caller may act as any `agent_id` — this is a research sandbox, not a venue.
- **Humans can't short; system bots can.** Keeps user accounting intuitive while letting liquidity bots always quote.
- **Bot orders are persisted like everyone else's**, so even resting quotes survive restarts; they're just excluded from reservation reconstruction.
- **Polling, not WebSockets**, keeps the stack simple; the snapshot endpoint is already shaped like a pub/sub payload if you want to add SSE/WS later.

## Ideas welcome

- WebSocket/SSE streaming instead of polling
- An agent SDK + tournament mode: strategies compete under the welfare objective
- More order types (post-only, iceberg), fee layers, a solidarity fund tax
- Alternative welfare metrics (Atkinson index, Nash social welfare) selectable per instance

---

*From each according to their ability, to each according to their needs — now with limit orders.*
