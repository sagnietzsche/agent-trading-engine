# trading-engine

A **mock exchange and an autonomous trading desk**. The venue is a from-scratch limit order book in **Go** (standard library `net/http`), persisted to **PostgreSQL** via **pgx** and watched through a **Bubble Tea terminal UI**. The desk is five **Claude agents** in Python — analyst, event strategist, portfolio manager, risk officer, execution trader — that observe the book, argue through typed contracts, and place real orders on it. No real money and no real market data: six fictional listings and a place to study how agent teams actually behave when they have to decide something.

> **The premise: agents make every decision.**
> The venue is neutral by default — strict price-time priority, no house view, nobody steering the outcome. Every trade that happens is one an agent chose, defended, and got past a risk officer. The exchange's job is to be a fair, fast, well-instrumented place for that to happen. The collective-welfare microstructure the project started with is still here, demoted to an [opt-in market regime](#market-regimes) you can switch agents into.

---

## Features

- **A five-agent LLM trading desk** — analyst → event strategist → portfolio manager → risk officer → execution trader, each with one job, one system charter, and one validated output schema. See [The agent desk](#the-agent-desk).
- **Least-privilege tool access** — only the execution trader holds tools that reach the exchange, and those tools refuse anything outside the tickets that already cleared risk.
- **Two-layer risk** — an LLM risk officer that can veto or shrink any ticket, then a deterministic limit checker in code that clamps it again. Judgement belongs to the model; authority belongs to code.
- **A replayable decision journal** — every cycle's briefing, reads, plan, risk cuts, and fills land in JSONL, so any session can be audited after the fact.
- **Cost and cache accounting** — per-agent token spend, wall time, and cache hit rate reported at the end of every run.
- **Two market regimes** — a conventional neutral exchange (default), or the original solidarity microstructure with giving mandates and need-priority matching, switchable per instance.
- **Bubble Tea terminal UI** — live floor: welfare gauge + trend, stocks, book ladder, tape, leaderboard, and the floor chatroom.
- **Tournament mode** — desks compete over a fixed number of ticks, scored on return (plus cooperation under the solidarity regime).
- **Market events** — loadable scenario definitions for shocks that would wreck a real market (recession, war, armageddon), fed to the event strategist as its forward-looking input.


## Table of contents

- [Features](#features)
- [Quickstart](#quickstart)
- [How it works](#how-it-works)
  - [Architecture](#architecture)
  - [Project layout](#project-layout)
  - [The matching engine](#the-matching-engine)
  - [Order lifecycle](#order-lifecycle)
  - [Persistence model](#persistence-model)
  - [Database schema](#database-schema)
  - [Market simulation](#market-simulation)
- [The agent desk](#the-agent-desk)
  - [The five seats](#the-five-seats)
  - [One cycle, end to end](#one-cycle-end-to-end)
  - [Two-layer risk](#two-layer-risk)
  - [Least-privilege execution](#least-privilege-execution)
  - [Prompt architecture and cost](#prompt-architecture-and-cost)
  - [The decision journal](#the-decision-journal)
  - [Swapping a seat](#swapping-a-seat)
- [Market regimes](#market-regimes)
  - [Welfare math](#welfare-math)
  - [Mandates](#mandates)
  - [Need-priority matching](#need-priority-matching)
  - [Neutral liquidity as a public good](#neutral-liquidity-as-a-public-good)
- [Tournament mode](#tournament-mode)
- [Python SDK reference](#python-sdk-reference)
- [Market events](#market-events)
- [API reference](#api-reference)
- [Terminal UI](#terminal-ui)
- [Configuration](#configuration)
- [Development](#development)
- [Design decisions & tradeoffs](#design-decisions--tradeoffs)
- [Ideas welcome](#ideas-welcome)


## Quickstart

Prereqs: Docker (for Postgres only), Go 1.27+, Python 3.10+, and Anthropic credentials for the desk.

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

Then put a desk on the floor. Every agent is a Claude model, so the desk needs
credentials — `ant auth login` stores a profile the SDK picks up automatically,
or export `ANTHROPIC_API_KEY`:

```bash
pip install -e sdk

# Watch the whole pipeline decide, without sending a single order.
trading-desk --name atlas --cycles 3 --dry-run

# For real, with the scenario files loaded and a journal on disk.
trading-desk --name atlas --cycles 20 \
    --events sdk/events --journal runs/atlas.jsonl
```

Each cycle prints what every seat concluded, what risk cut, and what filled;
the run ends with a per-agent token and cost breakdown. Afterwards,
`python sdk/examples/replay_journal.py runs/atlas.jsonl` walks the whole
session back — no API key required.

In the TUI, the desk shows up on the leaderboard as it trades:

1. Pick a welfare metric — **Gini coefficient**, **Atkinson index (ε = 0.5)**, or **Nash social welfare** — with ↑/↓ and enter. This is the venue's inequality read-out; under the default neutral regime it is pure observability.
2. If the server runs a different metric, the market reseeds under your choice, then the live floor renders: welfare gauge + trend, stocks, book, tape, and the leaderboard.
3. Switch symbols with ←/→, re-pick the metric anytime with `r`.
4. Press `c` to read the floor chat, and `a` to tell the floor something — the system agents answer in the feed.

If you want a clean slate later: `POST /api/admin/reset` wipes and reseeds everything (an optional `{"metric": …}` body also selects the welfare metric).


## How it works

### Architecture

```
┌────────────────────────────────────────────────────────────────────────────┐
│                                  CLIENTS                                   │
│                                                                            │
│  ┌───────────────────────┐          ┌────────────────────────────────┐     │
│  │ TUI — Go + Bubble Tea │          │ DESK — Python (trading_engine) │     │
│  │  model.go · view.go   │          │  analyst → strategist → PM →   │     │
│  │  client.go (WS)       │          │  risk → execution   (Claude)   │     │
│  └─────────────┬───────────┘        └──────────────────┬───────────────┘   │
│                │ HTTP /api · WS /api/ws                │ HTTP /api · WS    │
└────────────────┼───────────────────────────────────────┼───────────────────┘
                 │                                       │                    
                 ▼                                       ▼                    
┌────────────────────────────────────────────────────────────────────────────┐
│                                BACKEND (Go)                                │
│                                                                            │
│    ┌────────────────────┐  ┌────────────────────┐  ┌────────────────────┐  │
│    │ api.go — REST      │  │ ws.go — live feed  │  │ main.go — boot ·   │  │
│    │ routes · handlers  │  │ hub: one frame per │  │ migrations · sim   │  │
│    │ · DTOs · errors    │  │ symbol per tick,   │  │ loop (1 s) · http  │  │
│    │                    │  │ bounded send queue │  │ server · shutdown  │  │
│    └─────────┬──────────┘  └─────────┬──────────┘  └─────────┬──────────┘  │
│              │ 1. write path         │ 2. read path          │ 4. sim tick │
│              │                       │                       │             │
│              │                       │                       │             │
│              │                       │                       │             │
│              ▼                       ▼                       ▼             │
│   ┌──────────────────────────────────────────────────────────────────────┐ │
│   │ engine.go — Exchange (in-memory; no DB, no I/O)                      │ │
│   │ books · matching · reservations · welfare metrics · regimes          │ │
│   │ tournaments · chat · tape · snapshots — one sync.RWMutex             │ │
│   └───────────────────────────────┬──────────────────────────────────────┘ │
│                                   │ 1. DrainPending                        │
│                                   ▼                                        │
│   ┌──────────────────────────────────────────────────────────────────────┐ │
│   │ store.go — background flusher (the single writer)                    │ │
│   │ one transaction per batch · idempotent upserts                       │ │
│   └───────────────────────────────┬──────────────────────────────────────┘ │
│                                   │ 3. pgx — flush batches                 │
│                                   ▼                                        │
└───────────────────────────────────┼────────────────────────────────────────┘
                                    │                                         
                                    ▼                                         
                 ┌──────────────────────────────────────┐                     
                 │              PostgreSQL              │                     
                 │    agents · positions · orders ·     │                     
                 │     trades · welfare_snapshots ·     │                     
                 │             tournaments              │                     
                 └──────────────────────────────────────┘                     
```

- **Matching runs in memory** for speed. Each symbol has a price-time priority book. A `sync.RWMutex` guards the whole exchange: order placement and sim ticks take the exclusive lock, while read-model builders (REST reads, WS frame assembly) share the read lock so many clients can read concurrently.
- **Postgres is the source of truth for accounts and history.** Every mutation produces a batched, transactional flush (`Pending` buffer → upserts) that is handed to a **single background writer** — handlers never pay a synchronous DB round-trip. If a DB write fails the in-memory state stays consistent and the error is logged; the next mutation retries persistence.
- **Crash-safe restart**: on boot the engine loads agents/positions/open orders from Postgres, rebuilds the books, recomputes cash reservations, resumes order-id sequencing, and keeps trading. Kill it mid-session; nothing is lost.
- A background task ticks once per second: random-walk fair values, requote the market maker, refresh the depth provider (or fire solidarity flow, under that regime), advance tournaments and append a welfare snapshot.
- **Nothing in the backend trades on a view.** The two system agents quote and rest size; they never take. Every aggressive order on the venue came from an agent that decided to send it.
- **WebSocket streaming**: a broadcast hub assembles one snapshot frame **per subscribed symbol per tick** (plus a per-client desk for `agent_id` subscribers) and fans the marshaled bytes out to every connection — the per-tick cost is O(symbols + desks), not O(clients). Slow consumers get frames dropped, never the whole feed stalled.

> The numbers on the diagram are the data flows (write path, read path, pgx, sim tick, boot restore, events) — each is walked through lock by lock in **[docs/ARCHITECTURE.md](docs/ARCHITECTURE.md)** along with the matching sweep loop, welfare machinery, and WS hub.

### Project layout

```
trading-engine/
├── docker-compose.yml          # postgres:17-alpine, port 5432, healthcheck
├── .env.example                # DATABASE_URL / HOST / PORT / LOG_LEVEL / MARKET_REGIME / WELFARE_METRIC
├── backend/
│   ├── go.mod                  # pgx 5 · coder/websocket · google/uuid · godotenv
│   └── *.go
│       ├── main.go             # env, DB connect+migrate, boot/rebuild, sim loop, http.Server
│       ├── engine.go           # PURE matching + welfare + tournaments + chat (no DB)
│       ├── store.go            # pgx: connect/migrate/seed/flush/boot-load/reset/history
│       ├── api.go              # HTTP handlers + DTOs + error mapping (incl. tournaments, chat)
│       ├── ws.go               # /api/ws broadcast hub (one frame per symbol per tick)
│       ├── views.go            # read-models shared by REST & WS + LiveFrame builder
│       └── migrate.go          # SQL schema (ported from the original SeaORM migrations)
├── sdk/
│   ├── pyproject.toml          # pip install -e sdk · trading-desk CLI
│   ├── trading_engine/
│   │   ├── desk.py             # the orchestrator: one cycle, end to end
│   │   ├── agents/             # the five seats, one file each
│   │   │   ├── base.py         # DeskAgent: role, effort, charter
│   │   │   ├── analyst.py      # microstructure read → MarketRead
│   │   │   ├── strategist.py   # scenarios + floor → MacroRead
│   │   │   ├── pm.py           # the decision seat → PortfolioPlan
│   │   │   ├── risk.py         # veto/shrink → RiskAssessment
│   │   │   └── trader.py       # THE ONLY SEAT WITH TOOLS
│   │   ├── schemas.py          # the typed contract at every seam (pydantic)
│   │   ├── llm.py              # Claude client, model policy, cache, cost ledger
│   │   ├── risk.py             # deterministic limit checker — the final authority
│   │   ├── brief.py            # frame → compact briefing + the cached venue manual
│   │   ├── journal.py          # JSONL audit trail + rolling prompt memory
│   │   ├── client.py · ws.py   # REST client · live-frame stream
│   │   └── cli.py              # trading-desk
│   ├── events/                 # market event definitions (scenario + JSON)
│   │   ├── README.md           # event format reference
│   │   ├── GLOBAL_RECESSION.md # severity 6/10 — credit freeze + demand collapse
│   │   ├── WAR_LIKE_SITUATION.md # severity 8/10 — energy spike, supply chains crushed
│   │   └── ARMAGEDDON.md       # severity 10/10 — total loss of confidence
│   ├── examples/               # run-desk, replay-journal, custom-seat, inspect-event
│   └── tests/                  # pytest: risk clamp, full pipeline, tool gate, events
└── tui/
    └── *.go                    # Go + Bubble Tea terminal client (own module)
        ├── main.go             # entry: --backend flag / BACKEND_URL env
        ├── model.go            # bubbletea state machine: metric picker → live floor
        ├── view.go             # lipgloss panels: welfare, stocks, book, tape, agents, chat
        ├── client.go           # WS session: metric reseed, chat announce, frame streaming
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
- `chat` → deliberately **not** persisted: the floor chatroom is a session artifact (like the tape) and is wiped on restart

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

## The agent desk

The exchange is neutral, which leaves every decision to the agents trading on
it. The reference desk is five Claude agents that observe the same market and
hand work to each other through validated schemas.

The reason it is five agents and not one prompt: a single model asked to
"analyse, decide, and manage risk" does all three badly, because the roles want
different things. An analyst that is also accountable for P&L talks itself into
signals. A risk officer that also generates ideas approves its own. Splitting
the seats gives each one a narrow job it can actually be held to — and gives
the pipeline a place to fail loudly when a hand-off is wrong.

### The five seats

| Seat | Reads | Produces | Effort | Tools |
|---|---|---|---|---|
| **Market analyst** | book, tape, prices | `MarketRead` — per-symbol signals, conviction, fair value | high | none |
| **Event strategist** | scenario files, floor chat | `MacroRead` — narrative, severity, target exposure | high | none |
| **Portfolio manager** | both reads, desk state, journal | `PortfolioPlan` — concrete orders | high | none |
| **Risk officer** | plan, desk state, mandate | `RiskAssessment` — one verdict per ticket | medium | none |
| **Execution trader** | approved tickets, live book | fills | low | **place / cancel / read book** |

Effort is a per-role choice, not a global setting. The analyst is where the
edge is supposed to come from, so it thinks hard; the execution trader is
following an order that has already been argued over, so it runs cheap.

### One cycle, end to end

```
                        ┌──────────────────────┐
   GET /api/snapshot ──►│ briefing (brief.py)  │  spreads in bp, depth imbalance,
   GET /api/agents/:id  │ market + desk state  │  moves vs close — arithmetic done
                        └───────────┬──────────┘  so the model doesn't have to
                                    │
                 ┌──────────────────┴──────────────────┐   run concurrently:
                 ▼                                     ▼   independent inputs,
        ┌─────────────────┐                   ┌─────────────────┐  one wall-clock
        │ market analyst  │                   │event strategist │  wait
        │   MarketRead    │                   │    MacroRead    │
        └────────┬────────┘                   └────────┬────────┘
                 └──────────────────┬──────────────────┘
                                    ▼
                        ┌───────────────────────┐
                        │  portfolio manager    │◄── journal: last 4 cycles
                        │    PortfolioPlan      │    + realized equity change
                        └───────────┬───────────┘
                                    ▼
                        ┌───────────────────────┐
                        │     risk officer      │  may shrink or veto,
                        │    RiskAssessment     │  never enlarge
                        └───────────┬───────────┘
                                    ▼
                        ┌───────────────────────┐
                        │  enforce() — in code  │  arithmetic, not judgement
                        │   ApprovedTicket[]    │  cannot be argued with
                        └───────────┬───────────┘
                                    ▼
                        ┌───────────────────────┐
                        │  execution trader     │──► POST /api/orders
                        │  tool-calling loop    │    (gated by the tickets)
                        └───────────┬───────────┘
                                    ▼
                             journal (JSONL)  ──► next cycle's memory
```

A failure is contained to its cycle. A refusal, a rate limit, or a malformed
read loses that cycle's decision, gets written to the journal, and the desk
tries again on the next one. If the risk officer is unreachable, the desk
stands down rather than trading unchecked.

### Two-layer risk

The risk officer is a language model, and a language model asked to police
itself is a suggestion, not a control. So there are two layers:

```python
# 1. Judgement — catches what no static rule could.
assessment = risk_officer.run(market, desk, plan, limits, state, equity)
#    "cut to 40: the rationale cites a HELX signal the analyst never produced"

# 2. Arithmetic — catches the risk officer.
tickets, cuts = enforce(plan.trades, assessment.verdicts, desk, marks, limits, state)
#    single-order cap · per-name concentration · gross exposure · cash buffer
#    · free-share check (no borrow) · orders-per-cycle · drawdown kill switch
```

`enforce()` can only ever reduce a ticket, and it runs last. A test in the
suite makes the point directly: when the LLM risk officer waves through an
order 100× the size of the mandate, the code cuts it to 82 shares and logs why.

The drawdown kill switch is session-scoped and one-way — once the desk is
halted it stays halted, even if equity recovers, because the reason to stop was
never the current number.

### Least-privilege execution

Only the execution trader holds tools, and the tools are where authorization
lives — not the prompt:

```python
def place_limit_order(symbol, side, qty, limit_price) -> str:
    ticket, why = session.authorize(symbol, side, qty)
    if ticket is None:
        return why      # the model reads this and corrects; nothing was sent
    return session.submit(ticket, ...)
```

Asking to sell a name that was never approved, flipping a side, or exceeding a
ticket all return an error string the model can act on. A prompt is a request;
a function that checks its arguments before calling the exchange is a control.

### Prompt architecture and cost

Every agent's system prompt is two blocks: a **venue manual** — the rules of
the exchange, the pipeline, the standing instructions — followed by that seat's
**charter**. The manual is byte-identical for all five seats on every cycle, so
the cache breakpoint goes right after it and all five roles share one cached
prefix. Anything that changes tick to tick lives in the user turn instead.

That is a design constraint, not a nicety, and there is a test for it: a single
timestamp in the manual would silently drop the hit rate to zero and multiply
the bill. Cache performance is measured rather than assumed — every run ends
with the ledger:

```
role                   calls        in      out    cached      s      usd
------------------------------------------------------------------------
event_strategist           5     3,180    4,201    22,410   41.2    0.142
execution_trader           5     1,904    2,553    22,410   18.7    0.081
market_analyst             5     3,180    5,890    22,410   52.9    0.181
portfolio_manager          5     6,442    4,118    22,410   47.1    0.161
risk_officer               5     5,210    2,004    22,410   26.4    0.086
------------------------------------------------------------------------
total                     25                            71%        0.651
```

### The decision journal

Every cycle appends one JSON line: the reads, the plan, every risk cut with its
reason, the orders actually sent, and the errors. It is the audit trail — and
it is also the desk's only memory, since the last few cycles (with the realized
equity change after each) are rendered back into the PM's prompt. Without it
the PM re-decides from scratch every cycle and cannot tell that the trade it is
about to place is the same one that has lost money three cycles running.

```bash
python sdk/examples/replay_journal.py runs/atlas.jsonl
```

```
── cycle 4 · seq 218 · equity $98,412.55 · 11.3s ──
  analyst    : regime=mean_reverting
  strategist : elevated — energy shock still repricing through ZEPH and ORBT
  pm         : stance=risk_off, 2 proposed
      want sell 120 ORBT :: strategist flags severe_negative, we are 22% in it
      want buy 40 HELX :: defensive, mid 1.8% below fair with bid depth
  risk       : caution
      cut  buy 40 HELX: cut to 18 by risk — adding risk into an elevated tape
  trader     : sold 120 ORBT across two clips; skipped the HELX buy, ask thinned out
      SENT sell 60 ORBT → filled 60 @ 127.40
      SENT sell 60 ORBT → filled 60 @ 127.15
```

### Swapping a seat

An agent is a charter plus an output schema, so replacing one is a subclass:

```python
class DrawdownHawk(RiskOfficer):
    @property
    def charter(self) -> str:
        return super().charter + """

## Additional standing instruction — conservative mandate
* Halve any ticket whose rationale rests on a conviction below 0.6.
* Reject any market order.
"""

desk = TradingDesk(client, DeskConfig(name="hawk"))
desk.risk = DrawdownHawk(desk.llm, desk.manual)
desk.run()
```

Nothing else in the pipeline changes — the schema at the seam is what makes the
swap safe. Full example in [`sdk/examples/custom_seat.py`](sdk/examples/custom_seat.py).

## Market regimes

The venue runs one of two microstructures, chosen per instance via the
`MARKET_REGIME` env var or a `{"regime": …}` body on `POST /api/admin/reset`.

| | `neutral` (default) | `solidarity` |
|---|---|---|
| Matching | strict price-time priority | price-time, except solidarity orders route to the worst-off first |
| Mandates | none issued | every agent gets a suggested wealth-equalising trade |
| Second system agent | rests passive depth wide of the touch | executes its own giving mandate |
| Tournament score | `equity_return` | `equity_return + coop_share` |
| Welfare metrics | computed and published, purely observational | drive the mandate machinery |

The neutral regime is the default because "agents decide" and "the venue nudges
everyone toward equal shares" are different experiments, and mixing them makes
the first one unreadable. The solidarity machinery is intact, tested, and one
env var away — it is a genuinely interesting thing to point an agent desk at,
because the mandates are public information about where flow is headed:

```bash
MARKET_REGIME=solidarity go run ./backend
# or, against a running instance:
curl -X POST localhost:8080/api/admin/reset -d '{"regime":"solidarity"}'
```

Under solidarity, the desk's venue manual changes with it — the agents are told
mandates exist, that they are advisory, and that they signal where the venue is
about to push flow. What they do with that is their problem.

### The welfare machinery, in detail

Under the solidarity regime the exchange optimizes a **collective welfare function** instead of individual P&L. Four cooperating pieces:

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

Desks compete over a fixed number of ticks. What "winning" means depends on the
regime:

```
neutral     score = equity_return                      pure risk-taking skill
solidarity  score = equity_return + coop_share         cooperation counts too

equity_return = equity_end / equity_start − 1          baseline captured at start()
coop_share    = prosocial_volume / total_volume

A fill is *prosocial* for whichever side is WEALTHIER:
  richer seller → poorer buyer  ⇒ seller earns coop credit (giving a discount)
  richer buyer  → poorer seller ⇒ buyer earns coop credit (paying up)
```

Under solidarity, cooperation can fully offset a modest loss, so a pure
profit-maximizer can lose to a desk that trades with poorer members. Under the
neutral default the scoreboard is just P&L. Either way the interesting run is
several desks with different charters — a momentum reader against a
mean-reversion one, a hawk against a risk-taker — competing on the same book.

Lifecycle: **create → enter (while open) → start → runs N sim ticks (~1 s each)
→ finalize** — scores are persisted and running tournaments survive restarts.
Watch it live via the WS frame's `tournament` field.

## Python SDK reference

`sdk/` (Python ≥ 3.10) is both the desk and the client library underneath it.

| | |
|---|---|
| `TradingDesk` / `DeskConfig` | the orchestrator — runs the whole pipeline |
| `MarketAnalyst`, `EventStrategist`, `PortfolioManager`, `RiskOfficer`, `ExecutionTrader` | the five seats; subclass to swap one |
| `MarketRead`, `MacroRead`, `PortfolioPlan`, `RiskAssessment` | the pydantic contract at each seam |
| `RiskLimits` / `enforce()` | the mandate, and the code that enforces it |
| `DeskLLM` / `ModelPolicy` / `Ledger` | Claude client, per-role effort, token + cost accounting |
| `Journal` / `CycleRecord` | JSONL audit trail and rolling prompt memory |
| `venue_manual` / `market_brief` / `desk_brief` | the cached system prefix and the per-cycle briefing |
| `TradingClient` | typed REST wrapper over every endpoint |
| `WatchStream` | `/api/ws` live frames with automatic reconnect & resubscribe |
| `load_event` / `load_events` | market-event definitions (see below) |

```bash
pip install -e sdk

trading-desk --name atlas --cycles 20 --events sdk/events --journal runs/atlas.jsonl
trading-desk --name hawk --max-drawdown 0.10 --max-orders 3 --dry-run
```

```python
from pathlib import Path
from trading_engine import DeskConfig, RiskLimits, TradingClient, TradingDesk, load_events

desk = TradingDesk(
    TradingClient("http://127.0.0.1:8080"),
    DeskConfig(
        name="atlas",
        cycles=20,
        events=load_events("sdk/events"),
        journal_path=Path("runs/atlas.jsonl"),
        limits=RiskLimits(max_orders_per_cycle=3, max_drawdown_pct=0.12),
    ),
)
summary = desk.run()
```

The test suite runs without credentials: a stub stands in for every model call,
so the pipeline wiring, the risk clamp, and the execution tool gate are all
covered offline. See [`sdk/README.md`](sdk/README.md) for the full reference.

## Market events

Significant shocks that would wreck a real market — a global recession, a war-like situation, an armageddon — are defined as event files in [`sdk/events/`](sdk/events/README.md). Each one is a scenario document with a machine-readable JSON definition embedded in it: fair-value shocks per symbol, volatility and spread multipliers, a liquidity squeeze, circuit breakers, and how the solidarity flow should respond while the event runs.

Loaded scenarios are the **event strategist's** input — the only forward-looking
information anywhere on the desk. Pass `--events sdk/events` and that seat gets
the shock table, the headlines, and the stated mechanism, and has to work out
how much of it the tape has already priced in.

```python
from trading_engine import load_event, load_events

armageddon = load_event("sdk/events/ARMAGEDDON.md")
print(armageddon.headline())
# Armageddon (severity 10/10, systemic) · shock QNTM -80% .. ZEPH -45% · 300 ticks · …

all_events = load_events("sdk/events")
```

The loader validates definitions strictly — unknown fields and out-of-range values raise `EventError` at load time, so a typo can't silently do nothing. `python sdk/examples/inspect_event.py` prints an event's full effects table. The definitions are the contract an engine integration applies; firing them into the simulation is on the roadmap.

## API reference

All routes live under `/api`. Errors are `400/404/500` with `{"error": "message"}`.

| Method | Path | Description |
|---|---|---|
| GET | `/api/health` | liveness, database connectivity, and the instance's `regime` + `metric` |
| GET | `/api/ws?symbol=&agent_id=` | **WebSocket upgrade**: snapshot frames every ~1 s; client sends `{type:"subscribe",…}` |
| GET | `/api/snapshot?symbol=NOVA` | aggregate poll: welfare, stocks, book, last 40 trades, agents, active tournament, recent chat |
| GET | `/api/welfare` | welfare stats for the selected metric (`gini`/`atkinson`/`nash`, incl. raw `metric_value` and the active `regime`), target, mandates for every agent (empty under `neutral`), recent history (≤90 pts) |
| GET | `/api/stocks` | listings with last/bid/ask/change |
| GET | `/api/book/{symbol}?levels=10` | aggregated depth ladder |
| GET | `/api/trades?limit=50&symbol=` | recent tape (newest first, ≤400) |
| POST | `/api/agents` | register → `{agent_id, name, starting_cash}` ($100k) |
| GET | `/api/agents` | leaderboard sorted by equity, with roles |
| GET | `/api/agents/{id}` | full desk: balances, positions, open orders, and (under solidarity) the current mandate |
| GET | `/api/chat?limit=` | floor chatroom, newest first (ephemeral, in-memory) |
| POST | `/api/chat` | `{agent_id, text}` — an agent writes a chat message |
| POST | `/api/admin/announce` | `{text}` — broadcast an instruction; the system agents reply in chat |
| POST | `/api/orders` | place an order |
| DELETE | `/api/orders/{id}?agent_id=` | cancel one of your resting orders |
| POST | `/api/tournaments` | create `{name?, duration_ticks?}` (default 90 ticks ≈ 90 s) |
| GET | `/api/tournaments[/{id}]` | list / detail with live score previews |
| POST | `/api/tournaments/{id}/enter` | `{agent_id, strategy}` while open |
| POST | `/api/tournaments/{id}/start` | capture baselines, begin the countdown |
| POST | `/api/admin/reset` | wipe & reseed the whole market; optional body `{"metric": "gini"|"atkinson"|"nash", "regime": "neutral"|"solidarity"}` — omitted fields keep the current setting |

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

The API is deliberately small enough to drive from anything. A whole client is
four calls — register, read, decide, send:

```python
import requests, time
B = "http://localhost:8080/api"
me = requests.post(f"{B}/agents", json={"name": "manual"}).json()["agent_id"]
while True:
    snap = requests.get(f"{B}/snapshot", params={"symbol": "NOVA"}).json()
    nova = next(s for s in snap["stocks"] if s["symbol"] == "NOVA")
    # Buy when the mid sits below fair value; the market maker anchors to fair,
    # so displacement tends to revert.
    if nova["bid"] and nova["ask"] and (nova["bid"] + nova["ask"]) / 2 < nova["fair"] * 0.995:
        requests.post(f"{B}/orders", json={
            "agent_id": me, "symbol": "NOVA", "side": "buy",
            "kind": "limit", "qty": 10, "price": nova["ask"]})
    time.sleep(2)
```

## Terminal UI

A **Go + Bubble Tea** terminal client (`tui/`, its own module) watches the running engine over the same WebSocket feed the SDKs use — no browser, no extra server, just a terminal.

- **Session start** — a picker asks which welfare metric the session should optimize: Gini coefficient, Atkinson index (ε = 0.5), or Nash social welfare. If the server runs a different metric, the TUI reseeds the market via `POST /api/admin/reset {"metric": …}` and the floor rebuilds under the chosen metric.
- **Welfare bar** — the metric's headline value vs the solidarity target, mean equity, and a block-char inequality sparkline from the in-memory history.
- **Stocks** — listings with last / change% / bid / ask.
- **Book ladder** — depth for the selected symbol with size bars and a spread readout.
- **Time & sales** — last prints colored ▲ green when wealth moved to a poorer agent, ▼ red when it moved to a richer one.
- **Agents** — leaderboard with role chips (contributor / beneficiary / neutral).
- **Chat room** (`c`) — the floor chatroom: the market maker calls out sharp moves, the second system agent answers questions about its regime, desks post what they did (`client.say(...)`), and tournaments announce themselves.
- **Announce** (`a`) — type an instruction and broadcast it to the floor; the system agents answer it in the chat feed (try "give 5%" or "hello").

Keys: `←/→` switch symbol, `c` toggle the chat room, `a` announce to the floor, `r` re-pick the metric (starts a fresh session), `q` / `ctrl+c` quit. `--backend` flag or `BACKEND_URL` env points it at the API (default `http://127.0.0.1:8080`). Frames are pushed once per second; a dropped connection reconnects automatically, with the status line reflecting the feed state. Chat is ephemeral — like the tape, it lives in memory for the session and is wiped on restart.

## Configuration

Environment (loaded from `.env` via `godotenv`):

| Var | Default | Meaning |
|---|---|---|
| `DATABASE_URL` | — (required) | e.g. `postgres://trading:trading@localhost:5432/trading` |
| `HOST` | `127.0.0.1` | bind address |
| `PORT` | `8080` | bind port |
| `LOG_LEVEL` | `info` | logging filter |
| `MARKET_REGIME` | `neutral` | microstructure for the instance: `neutral` \| `solidarity` (overridable per session via `POST /api/admin/reset`) |
| `WELFARE_METRIC` | `gini` | welfare statistic for the instance: `gini` \| `atkinson` \| `nash` (overridable per session via the TUI / reset) |

The desk reads Anthropic credentials the way the SDK does — `ANTHROPIC_API_KEY`,
then `ANTHROPIC_AUTH_TOKEN`, then an `ant auth login` profile. It never needs a
key in a config file. Model and risk mandate are `trading-desk` flags rather
than env vars, so two desks on the same machine can differ.

Engine tuning constants (recompile to change): `GINI_TARGET`, `ROLE_THRESHOLD`, `GIFT_RATE`, sim tick interval (1 s), MM quote levels/spread, starting cash, tape depth cap (400).

## Development

```bash
cd backend && go test ./...   # engine + API unit tests — no database required
cd backend && go run .        # needs Postgres (docker compose up -d)
cd tui && go test ./...       # rendering + session integration tests
pip install -e 'sdk[dev]' && python -m pytest sdk/tests  # desk + risk + events, no API key needed
```

**The SDK suite runs offline.** Every model call is replaced by a stub that
returns canned schema instances, which is exactly what the typed seams between
agents are for — if the contract holds for a stub, it holds for the model. That
covers the pipeline wiring, the parallel analysis stage, the risk clamp, the
execution tool gate, and the journal, with no credentials and no network.

Test coverage highlights:

- **Engine** — Gini / Atkinson / Nash math (incl. the textbook Gini 0.25 case), price-time priority sweeps, partial-fill/rest behavior, self-trade prevention, reservation accounting on place/fill/cancel (both sides), balance rejection paths, MM requote bounding book growth, welfare snapshots per tick, boot-time restore rebuilding books + reservations + id sequence, the tournament lifecycle, the chatroom.
- **Regimes** — mandate direction under solidarity (contributor→sell, beneficiary→buy, neutral→none), need-priority routing past better-priced neutral quotes, sustained gifting reaching those who ask, and the neutral counterpart: no mandates issued, and a solidarity-flagged order taking the *best price* rather than the neediest counterparty.
- **Desk** — the full five-seat cycle, dry run, a risk halt blocking every ticket, an oversized plan being clamped by code after the LLM risk officer approved it, an empty plan as a valid cycle, journal recall feeding the next cycle.
- **Execution gate** — the trader cannot trade an unapproved symbol, flip a side, or exceed a ticket; slicing within a ticket works until it is worked; denials are recorded.
- **Risk clamp** — every limit in isolation, plus the drawdown kill switch staying halted after equity recovers.
- **Prompt hygiene** — the venue manual is byte-stable and carries no wall clock, so the cached prefix cannot silently invalidate.

Want to help? See **[CONTRIBUTING.md](CONTRIBUTING.md)** — module conventions, the docs that must stay in sync, and the PR checklist.

## Design decisions & tradeoffs

- **In-memory hot path + durable ledger** mirrors how real exchanges work: matching latency doesn't pay a DB tax, but every state change is journaled. Consistency window: a crash between mutation and flush loses at most the last in-flight batch.
- **One engine, one writer, many readers.** Matching stays single-threaded behind an exclusive lock (order-book engines are inherently sequential — sharding per symbol would be the next scale step), but every read path shares the lock via `RWMutex`, the WebSocket feed assembles frames once per symbol instead of once per client, and persistence runs on a dedicated background writer. The hot path is CPU- and memory-bound, not DB- or lock-bound.
- **`float64` in the engine, `NUMERIC(20,4)` at rest.** Prices are cent-rounded at entry and costs at settlement; float dust never reaches the ledger.
- **No authentication.** Any caller may act as any `agent_id` — this is a research sandbox, not a venue.
- **Humans can't short; system bots can.** Keeps user accounting intuitive while letting liquidity bots always quote.
- **Bot orders are persisted like everyone else's**, so even resting quotes survive restarts; they're just excluded from reservation reconstruction.
- **The venue is neutral; the agents are the interesting part.** The exchange has no view, and its two system agents quote but never take. Everything that happens because someone wanted it to happen came from an agent — which is the only way the desk's decisions are legible as decisions.
- **Judgement in the model, authority in code.** An LLM risk officer catches things no static rule would; a static rule catches the risk officer. Neither layer is sufficient and the ordering matters — arithmetic runs last.
- **Authorization in tools, not prompts.** The execution trader's tools validate every argument against the approved tickets before calling the exchange. Prompt instructions are requests; a function that refuses is a control.
- **Typed seams between agents.** Every hand-off is a pydantic schema, so a drifting agent fails at the boundary instead of three steps later inside the matching engine — and the whole pipeline is testable with stubs.
- **One long cached prefix, measured.** All five seats share a byte-identical venue manual behind a cache breakpoint, and the run reports its actual hit rate. Caching you don't measure is caching you don't have.
- **The TUI is a client, not a fork.** It watches the running engine through the same HTTP + WebSocket API the SDKs use, so scripts and the terminal always see the same market — and the metric choice is a session property, applied by reseeding when needed, rather than a recompile.
- **Chat is ephemeral, like the tape.** The floor chatroom lives in memory for the session and is wiped on restart: it's conversation, not ledger. Every market event that matters (fills, snapshots, tournaments) is still persisted.

## Ideas welcome

- ~~Alternative welfare metrics (Atkinson index, Nash social welfare) selectable per instance~~ ✅ shipped — pick one in the TUI
- ~~A terminal UI in place of the browser frontend~~ ✅ shipped — Go + Bubble Tea, metric picker at session start
- ~~A floor chatroom where agents report what they did~~ ✅ shipped — `c` to watch, `a` to announce
- ~~A neutral market regime where agents, not the venue, make every decision~~ ✅ shipped — `MARKET_REGIME=neutral` is now the default
- ~~An LLM agent desk with separated roles and real risk controls~~ ✅ shipped — `trading-desk`
- Wire market events into the engine so firing one actually shocks the simulation (fair values, volatility, spread, liquidity)
- Multi-desk tournaments: several charters on the same book, scored head to head
- A backtest harness that replays a journal against a recorded tape, so a charter change can be evaluated without paying for a live run
- Trade from the TUI: join as an agent, auto-fill the ticket with your mandate, place orders with the keyboard
- Tournament panels in the TUI: a live scoreboard while a competition runs
- More order types (post-only, iceberg), fee layers, a solidarity fund tax
