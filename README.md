# agentic-trading-engine

An open-source **mock** trading engine built from scratch with **Go** (standard library `net/http`), watched through a **Bubble Tea terminal UI**, and backed by **PostgreSQL** through **pgx**.

AI agents connect through an HTTP API and trade six fictional stocks against each other. There is no real money and no real-time market data — it is a playground for studying how trading agents behave.

> **The twist: agents are not rewarded for greed.**
> The exchange has a collective objective baked into its microstructure. Inequality is measured continuously, surplus agents receive giving mandates, and designated solidarity orders are matched to the worst-off members *first*. See [Solidarity mechanism](#solidarity-mechanism).


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
- [Terminal UI](#terminal-ui)
- [Configuration](#configuration)
- [Development](#development)
- [Design decisions & tradeoffs](#design-decisions--tradeoffs)
- [Ideas welcome](#ideas-welcome)


## Quickstart

Prereqs: Docker (for Postgres only), Go 1.22+.

```bash
# 1. Start Postgres
docker compose up -d

# 2. Configure env (defaults match docker-compose)
cp .env.example .env

# 3. Run the backend — migrations apply automatically on first boot,
#    listings + system bots get seeded, and books open for business.
cd backend && go run .          # listens on :8080

# 4. In another terminal, run the TUI
cd tui && go run .              # talks to :8080 over WebSocket
```

In the TUI:

1. Pick a welfare metric — **Gini coefficient**, **Atkinson index (ε = 0.5)**, or **Nash social welfare** — with ↑/↓ and enter.
2. If the server runs a different metric, the market reseeds under your choice, then the live floor renders: welfare gauge + trend, stocks, book, tape, and the leaderboard.
3. Switch symbols with ←/→, re-pick the metric anytime with `r`.

If you want a clean slate later: `POST /api/admin/reset` wipes and reseeds everything (an optional `{"metric": …}` body also selects the welfare metric).


## How it works

### Architecture

```
┌──────────┐  HTTP /api    ┌───────────────────────────────┐   write-through   ┌───────────┐
│   TUI    │ ────────────► │  Go net/http                 │ ────────────────► │ Postgres  │
│ bubbletea│ ◄──────────── │  ├─ matching engine (in-mem)  │  (pgx)           │ via pgx   │
└──────────┘  WS frames    │  ├─ welfare / mandates        │ ◄──────────────── │           │
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
│   ├── pyproject.toml          # pip install -e sdk · trading-agent CLI
│   ├── trading_engine/         # python client: {client,ws,agent,cli,events}.py
│   ├── events/                 # market event definitions (scenario + JSON)
│   │   ├── README.md           # event format reference
│   │   ├── GLOBAL_RECESSION.md # severity 6/10 — credit freeze + demand collapse
│   │   ├── WAR_LIKE_SITUATION.md # severity 8/10 — energy spike, supply chains crushed
│   │   └── ARMAGEDDON.md       # severity 10/10 — total loss of confidence
│   ├── examples/               # mandate-bot, greedy-vs-cooperative, inspect-event
│   └── tests/                  # pytest: event loader + validation
└── tui/
    └── *.go                    # Go + Bubble Tea terminal client (own module)
        ├── main.go             # entry: --backend flag / BACKEND_URL env
        ├── model.go            # bubbletea state machine: metric picker → live floor
        ├── view.go             # lipgloss panels: welfare, stocks, book, tape, agents
        ├── client.go           # WS session: metric reseed + frame streaming + reconnect
        └── *_test.go           # rendering + session integration tests
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

Every fill produces a **trade record**: maker price, quantity, buyer, seller, taker order id, plus welfare context — both parties' equity just before settlement and the network-wide **inequality index right after**, measured in whatever metric the instance has selected.

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
| `trades` | `id uuid pk, symbol fk→stocks, price, qty, buyer fk→agents, seller fk→agents, taker_order bigint fk→orders, buyer_equity, seller_equity, gini_after numeric(10,6), ts` | indexed `(symbol, ts)`; `gini_after` holds the instance's metric |
| `positions` | `(agent_id, symbol) pk, qty int` | qty may be negative for system bots |
| `welfare_snapshots` | `id bigserial pk, gini numeric(10,6), metric text, metric_value numeric(22,4), total_equity numeric(22,4), mean_equity numeric(20,4), ts` | one row per sim tick; `metric` self-describes the series |
| `tournaments` | `id uuid pk, name, status, duration_ticks, ticks_left, gini_start numeric(10,6), gini_final null, created_at, started_at null, finished_at null` | competition sessions |
| `tournament_entries` | `(tournament_id, agent_id) pk fk→agents, strategy, start_equity, total_volume, prosocial_volume, return_pct null, coop_share null, score null, finished_at null` | enrolled strategies & results |

FK constraints enforce that trades/orders/positions always reference real agents and listings.

### Market simulation

A spawned task runs `sim_tick()` every second:

1. **Random walk** each symbol's fair value: small drift `U(-0.0015, +0.002)` plus a fat-tailed shock (cubed uniform noise), clamped to sane bounds.
2. **Requote the market maker** — cancels all of its resting quotes, then posts 3 bid + 3 ask levels around fair value with spread `max(fair × 0.0015, $0.01)` and randomized sizes (20–90 shares). Cancel-then-repost keeps books bounded.
3. **Solidarity flow** — while measured inequality (in the instance's metric) exceeds the target, the `solidarity_bot` executes its own giving mandate as a *solidarity order* (see below).
4. **Snapshot welfare** (inequality index, metric, total equity, mean equity) into `welfare_snapshots` — this feeds the TUI trend sparkline and `/api/welfare` history.

System agents (fixed UUIDs, flagged `is_bot`):

| Agent | Role | Endowment |
|---|---|---|
| `market_maker` | neutral two-sided quoting | $10M cash (shorts freely) |
| `solidarity_bot` | automated redistribution | $6M cash + 40k shares of every listing — watching it give that away is the demo |

## Solidarity mechanism

The exchange optimizes a **collective welfare function** instead of individual P&L. Four cooperating pieces:

### Welfare math

Agent equities are marked continuously and inequality is summarized with one of **three selectable metrics** over the population. The instance's choice — the TUI session picker, or the `WELFARE_METRIC` env var at boot — drives every downstream number: mandates, the tape's per-fill context, snapshots, and tournament bookends, so a session's whole ledger speaks the same statistic.

**Gini coefficient** (default):

```
gini = Σᵢ (2i − n − 1)·xᵢ / (n · Σ xⱼ)        (x sorted ascending, i = 1..n)
```

**Atkinson index** (ε = 0.5):

```
A = 1 − [(1/n)Σᵢ (xᵢ/μ)^(1−ε)]^(1/(1−ε))
```

More sensitive to the poorest members than Gini: a dollar to the worst-off agent lowers it more than the same dollar to a rich one.

**Nash social welfare**: the geometric mean of equities, `NSW = (∏xᵢ)^(1/n)`. It is normalized into the same inequality framing as the other two: `deficit = 1 − NSW/μ ∈ [0,1)`, with `0` = everyone holds the mean (that's the AM–GM inequality). The API surfaces the raw `NSW` as `metric_value` alongside the deficit.

For every metric, `0` = everyone holds equal wealth, `1` = one agent owns everything. Every fill stores the equities involved and the post-trade index, so the tape itself becomes an inequality ledger. Knobs live at the top of `engine.go`:

| Constant | Default | Meaning |
|---|---|---|
| `GINI_TARGET` | `0.20` | above this, redistribution flow activates (for whichever metric is selected) |
| `ROLE_THRESHOLD` | `0.10` | ±deviation-from-mean that assigns a role |
| `GIFT_RATE` | `0.05` | fraction of one's wealth gap offered per mandate |

### Mandates

`GET /api/welfare` publishes, for **every** agent, a computed role and an executable suggestion:

- **Contributor** (>10% above mean) → *give*: sell inventory from your largest holding, priced **at the current bid** so it crosses immediately. Selling at the bid instead of the mid is the concession — the gift is priced in.
- **Beneficiary** (>10% below mean) → *receive*: use up to 5% of your shortfall to acquire assets **at the ask**, sized to what your free cash affords.
- **Neutral** (within ±10%) → hold steady; no suggestion issued.

Each suggestion carries a plain-language rationale ("You hold +42.1% vs the mean…"), which the TUI surfaces verbatim.

This endpoint is deliberately shaped as a hook: a future autonomous agent's entire strategy can be *"poll my mandate and comply"* — or not, and then watch what inequality does.

### Need-priority matching

Ordinary orders match by strict price-time priority. Orders placed through `place_solidarity_order` add one twist, enforced **inside the matcher**: candidate counterparties whose owners sit more than `ROLE_THRESHOLD` below the mean are matched **first**, regardless of price. Only when nobody needs help does normal priority resume.

Consequence: a gift can't be intercepted by a better-priced neutral quote or by the market maker. When a poor member has a resting buy, solidarity supply lands on *them*.

The `solidarity_bot` uses this pathway every tick while measured inequality (in the instance's metric) is above target — it sells its inventory into the bids of the worst-off members, converting its surplus into their assets. Its endowment is deliberately large so the giving is visible.

### Neutral liquidity as a public good

The market maker takes no directional view and earns nothing by design beyond churn; it exists so that *everyone* trades at tighter spreads. It also serves as the counterparty of last resort so books are never empty — but need-priority routing makes sure charity doesn't just bounce off it.

The TUI reflects the objective everywhere: the metric's headline value vs target plus trend sparkline, per-agent role chips, and tape prints colored ▲ green when wealth moved toward equality / ▼ red when it moved away.

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
Watch it live via the WS frame's `tournament` field.

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

## API reference

All routes live under `/api`. Errors are `400/404/500` with `{"error": "message"}`.

| Method | Path | Description |
|---|---|---|
| GET | `/api/health` | liveness + database connectivity |
| GET | `/api/ws?symbol=&agent_id=` | **WebSocket upgrade**: snapshot frames every ~1 s; client sends `{type:"subscribe",…}` |
| GET | `/api/snapshot?symbol=NOVA` | aggregate poll: welfare, stocks, book, last 40 trades, agents, active tournament |
| GET | `/api/welfare` | welfare stats for the selected metric (`gini`/`atkinson`/`nash`, incl. raw `metric_value`), target, mandates for every agent, recent history (≤90 pts) |
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
| POST | `/api/admin/reset` | wipe & reseed the whole market; optional body `{"metric": "gini"|"atkinson"|"nash"}` selects the welfare metric |

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

## Terminal UI

The browser frontend is replaced by a **Go + Bubble Tea** terminal client (`tui/`, its own module). It connects to the running engine over the same WebSocket feed the SDKs use.

- **Session start** — a picker asks which welfare metric the session should optimize: Gini coefficient, Atkinson index (ε = 0.5), or Nash social welfare. If the server runs a different metric, the TUI reseeds the market via `POST /api/admin/reset {"metric": …}` and the floor rebuilds under the chosen metric.
- **Welfare bar** — the metric's headline value vs the solidarity target, mean equity, and a block-char inequality sparkline from the in-memory history.
- **Stocks** — listings with last / change% / bid / ask.
- **Book ladder** — depth for the selected symbol with size bars and a spread readout.
- **Time & sales** — last prints colored ▲ green when wealth moved to a poorer agent, ▼ red when it moved to a richer one.
- **Agents** — leaderboard with role chips (contributor / beneficiary / neutral).

Keys: `←/→` switch symbol, `r` re-pick the metric (starts a fresh session), `q` / `ctrl+c` quit. `--backend` flag or `BACKEND_URL` env points it at the API (default `http://127.0.0.1:8080`). Frames are pushed once per second; a dropped connection reconnects automatically, with the status line reflecting the feed state.

## Configuration

Environment (loaded from `.env` via `godotenv`):

| Var | Default | Meaning |
|---|---|---|
| `DATABASE_URL` | — (required) | e.g. `postgres://trading:trading@localhost:5432/trading` |
| `HOST` | `127.0.0.1` | bind address |
| `PORT` | `8080` | bind port |
| `LOG_LEVEL` | `info` | logging filter |
| `WELFARE_METRIC` | `gini` | welfare statistic for the instance: `gini` \| `atkinson` \| `nash` (overridable per session via the TUI / reset) |

Engine tuning constants (recompile to change): `GINI_TARGET`, `ROLE_THRESHOLD`, `GIFT_RATE`, sim tick interval (1 s), MM quote levels/spread, starting cash, tape depth cap (400).

## Development

```bash
cd backend && go test ./...   # engine + API unit tests — no database required
cd backend && go run .        # needs Postgres (docker compose up -d)
cd tui && go test ./...       # rendering + session integration tests
pip install -e sdk[dev] && python -m pytest sdk/tests   # event loader + validation
```

Test coverage highlights: Gini / Atkinson / Nash math (incl. the textbook Gini 0.25 case), price-time priority sweeps, partial-fill/rest behavior, self-trade prevention, reservation accounting on place/fill/cancel (both sides), balance rejection paths, mandate direction (contributor→sell, beneficiary→buy, neutral→none), need-priority routing past better-priced neutral quotes, sustained gifting reaching those who ask, MM requote bounding book growth, welfare snapshots per tick, boot-time restore rebuilding books + reservations + id sequence, and the tournament lifecycle (scoring formula, prosocial attribution to the wealthier side, double-entry/start rejection, finalize-once persistence queue, restore of running competitions).

## Design decisions & tradeoffs

- **In-memory hot path + durable ledger** mirrors how real exchanges work: matching latency doesn't pay a DB tax, but every state change is journaled. Consistency window: a crash between mutation and flush loses at most the last in-flight batch.
- **One engine, one writer, many readers.** Matching stays single-threaded behind an exclusive lock (order-book engines are inherently sequential — sharding per symbol would be the next scale step), but every read path shares the lock via `RWMutex`, the WebSocket feed assembles frames once per symbol instead of once per client, and persistence runs on a dedicated background writer. The hot path is CPU- and memory-bound, not DB- or lock-bound.
- **`float64` in the engine, `NUMERIC(20,4)` at rest.** Prices are cent-rounded at entry and costs at settlement; float dust never reaches the ledger.
- **No authentication.** Any caller may act as any `agent_id` — this is a research sandbox, not a venue.
- **Humans can't short; system bots can.** Keeps user accounting intuitive while letting liquidity bots always quote.
- **Bot orders are persisted like everyone else's**, so even resting quotes survive restarts; they're just excluded from reservation reconstruction.
- **The TUI is a client, not a fork.** It watches the running engine through the same HTTP + WebSocket API the SDKs use, so scripts and the terminal always see the same market — and the metric choice is a session property, applied by reseeding when needed, rather than a recompile.

## Ideas welcome

- ~~Alternative welfare metrics (Atkinson index, Nash social welfare) selectable per instance~~ ✅ shipped — pick one in the TUI
- ~~A terminal UI in place of the browser frontend~~ ✅ shipped — Go + Bubble Tea, metric picker at session start
- More order types (post-only, iceberg), fee layers, a solidarity fund tax
- Trade from the TUI: join as an agent, auto-fill the ticket with your mandate, place orders with the keyboard
- Tournament panels in the TUI: a live scoreboard while a competition runs
