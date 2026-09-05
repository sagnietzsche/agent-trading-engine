# Architecture deep dive

How the pieces actually work, under the hood. This assumes you have read
[README.md](../README.md); here we trace the code paths, lock by lock, tick by
tick. File references are relative to `backend/`.

---

## 1. The big picture

```
┌────────────────────────────────────────────────────────────────────────────┐
│                                  CLIENTS                                   │
│                                                                            │
│  ┌───────────────────────┐          ┌────────────────────────────────┐     │
│  │ TUI — Go + Bubble Tea │          │ DESK — Python (trading_engine) │     │
│  │  model.go · view.go   │          │  analyst → strategist → PM →   │     │
│  │  client.go (WS)       │          │  risk → execution  (§9, Claude)│     │
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
│   │ books · matching · reservations · welfare metrics · regimes (§4)     │ │
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

- **One `Exchange` struct** owns everything mutable: books, agents, orders,
  the tape, welfare history, tournaments, chat. It is guarded by a single
  `sync.RWMutex` (`lockedExchange` in `main.go`).
- **Writers** (order placement, sim ticks, admin actions) hold the exclusive
  lock, mutate, drain the `Pending` buffer, release the lock, and hand the
  batch to a **single background flusher** that persists it to Postgres.
- **Readers** (REST handlers, the WS hub) hold the shared lock only long
  enough to build a read-model, then marshal/stream outside the lock.
- **Postgres is the durable ledger**; the in-memory exchange is the hot path.
  On boot, the exchange is rebuilt from Postgres (`bootExchange` → `Restore`).
- **The backend has no trading view.** Under the default neutral regime it is a
  fair, fast venue and nothing more — see §4. Every decision belongs to a client
  like the agent desk in §9, which gets no privileged access.

### Data flows (the numbers on the diagram)

1. **Write path** — order placement, chat, admin actions, tournament commands.
   `POST /api/orders` → handler validates → `ex.lock()` → reserve + match in
   `placeOrderInner` → `DrainPending()` → `ex.unlock()` → `submitFlush()` → the
   background flusher queues a batch → Postgres. The response is sent after the
   unlock; handlers never pay a DB round-trip.
2. **Read path** — REST reads (`/api/snapshot`, `/api/chat`, history) and the
   WS feed. The handler or hub takes `rlock()`, builds the read-model from
   `views.go`, releases the lock, and only then marshals and responds.
3. **pgx** — the flusher is the *only* writer to Postgres: one transaction per
   batch, idempotent upserts. On boot the same layer reads back (`Restore`)
   to rebuild the in-memory exchange.
4. **Sim tick (1 s)** — `main.go`'s ticker takes `lock()`, runs `SimTick()`
   (random-walk fair values, MM requote, solidarity flow, tournament
   countdown, welfare snapshot, system chat), drains the batch and submits it.
5. **Boot restore** — `connectDB` → `migrate` → seed if empty → `bootExchange`
   (`Restore`) rebuilds agents, positions, open orders (replayed in id order),
   recomputes reservations, resumes the id sequence and reloads running
   tournaments, then the sim loop and HTTP server start.
6. **SDK market events** — `sdk/events/*.md` are loaded and validated by
   `events.py`. Definitions only; the engine hooks are not wired yet (§8).

---

## 2. The order lifecycle

`POST /api/orders` traces the full write path:

```
Client          Handler (api.go)        Exchange (engine.go)            Flusher (store.go)          Postgres
   |  POST /api/orders     |                   |                              |                          |
   |---------------------->|  decode body; parse side/kind                   |                          |
   |                       |  ex.lock()        |                             |                          |
   |                       |------------------>| placeOrderInner             |                          |
   |                       |                   | 1. validate agent/symbol/   |                          |
   |                       |                   |    qty/price                |                          |
   |                       |                   | 2. reserve: buy → cash,     |                          |
   |                       |                   |    sell → shares (humans)   |                          |
   |                       |                   | 3. execute(): match loop    |                          |
   |                       |                   |    (see §3)                 |                          |
   |                       |                   | 4. status + rest remainder  |                          |
   |                       |                   |    in book / release unused |                          |
   |                       |                   |    reservation              |                          |
   |                       |                   | 5. touch agent + record     |                          |
   |                       |  DrainPending()   |                             |                          |
   |                       |  ex.unlock()      |                             |                          |
   |                       |  submitFlush(pending) ------------------------->|  queue job               |
   |                       |                   |                             |------------------------->|  BEGIN
   |                       |                   |                             |   agents/positions      |  upserts
   |                       |                   |                             |   orders/trades         |  (one tx,
   |                       |                   |                             |   snapshots             |   ON CONFLICT
   |                       |                   |                             |   tournaments           |   DO UPDATE)
   |  <---------------------|  201 {order, fills, free_cash}                 |------------------------->|  COMMIT
```

Key rules:

- **Reserve up-front, release on settle.** A buy locks `price × qty` cash
  (market buys reserve `best_ask × qty × 1.001` slippage buffer); a sell locks
  shares. When the order rests, the reservation shrinks to the resting
  remainder; on cancel it is released entirely. You cannot place two orders
  that spend the same dollar.
- **Status transitions** (persisted on every row):
  `open → partially_filled → filled | cancelled`.
- **The handler never pays a DB round-trip.** Persistence is the flusher's job;
  a DB failure just logs and the next mutation retries.

---

## 3. The matching engine

### Book structure and priority

Each symbol's book is a slice of `RestingOrder` kept in insertion order.
Priority is computed on demand via a **book key**:

```
bookKeyFor(side, price, id):
  ticks = round(price * 100)
  buy  (bid) : rank = MaxUint64 − ticks     // higher price → smaller rank → first
  sell (ask) : rank = ticks                 // lower price → smaller rank → first
  tie-break  : id (order ids ascend with time → older first)
```

So matching is *price-time priority*: best price first, then arrival order.

### The sweep loop

One taker order fills against as many makers as it can, in one `execute()`
call:

```
execute(): taker qty = 130, market buy NOVA

  loop 1   candidates: 100.00×50 (bob) · 100.00×50 (carol) · 100.50×40 (bob)
           pick best by book key → 100.00×50 bob → fill 50 @ 100.00
  loop 2   qty=80 → pick 100.00×50 carol → fill 50 @ 100.00
  loop 3   qty=30 → pick 100.50×40 bob    → fill 30 @ 100.50
  loop 4   qty=0 → done

  per fill:
    • skip the taker's own resting orders (no wash trades)
    • release the maker's reservation for the filled amount
    • capture both parties' pre-settlement equity (welfare context)
    • book.reduce(maker); keep maker's OrderRecord in sync (filled/status)
    • settle: buyer cash −= cost, +shares; seller cash += cost, −shares
    • tournament attribution: fill is prosocial for the WEALTHIER side
    • compute inequality_after (the instance's welfare metric) → Trade
    • front-push the Trade onto the tape (capped at 400)
```

### Market orders (IOC)

A market order has no limit: it sweeps whatever crosses and **cancels the
unfilled remainder**. If there is no opposite-side liquidity at all, it is
rejected with `no liquidity on the opposite side of the book`.

### Solidarity routing (solidarity regime only)

`PlaceSolidarityOrder` marks the taker order as solidarity. Under
`RegimeSolidarity`, the engine computes the set of **beneficiaries** before
matching — agents whose equity sits more than `ROLE_THRESHOLD` below the mean.
During the sweep, candidate counterparties in that set are matched **first,
regardless of price**; only when nobody needs help does normal price-time
priority resume. This is how a gift can't be intercepted by the market maker or
a better-priced neutral quote.

Under the default `RegimeNeutral` the flag is **inert**: `execute()` gates the
beneficiary computation on `ex.solidarityEnabled()`, so a solidarity-marked
order gets exactly the same price-time priority as everything else. That is
asserted directly in `TestNeutralRegimeIssuesNoMandatesAndNoNeedPriority`.

---

## 4. Market regimes and the welfare machinery

### The regime switch

`MarketRegime` is instance-level configuration (`MARKET_REGIME` env, or the
`{"regime": …}` body of `POST /api/admin/reset`), resolved once into
`Exchange.regime` and read everywhere through one predicate:

```go
func (ex *Exchange) solidarityEnabled() bool {
    return ex.regime.normalized() == RegimeSolidarity
}
```

Everything that expresses a house view hangs off it:

| Behaviour | `neutral` (default) | `solidarity` |
|---|---|---|
| `Mandates()` | returns `nil` | one mandate per agent |
| `execute()` need-priority | skipped | beneficiaries matched first |
| `tournamentScore()` | `return` only | `return + coop_share` |
| Second system agent (`SolidarityID`) | `quoteDepthTick()` — rests passive size 5–9 ticks out, never crosses | `redistributeTick()` — executes its own giving mandate |
| Its display name | `depth_bot` | `solidarity_bot` |
| `Welfare()` | still computed and published, purely observational | drives the mandate machinery |

Welfare metrics are computed under **both** regimes. A neutral venue still
reports how concentrated wealth is; it just does nothing about it. `Welfare`
carries a `regime` field so a client can tell whether the mandate machinery
behind those numbers is live.

The default is neutral because "agents make every decision" and "the venue
nudges everyone toward equal shares" are separate experiments — mixing them
makes the first one unreadable.

### Metrics

Three selectable metrics (`WelfareMetric`), all reduced to an **inequality
index in [0, 1]** — that single number drives everything downstream:

| Metric | Computation | Notes |
|---|---|---|
| `gini` (default) | classic Gini coefficient | `0` = equal, `1` = one agent owns everything |
| `atkinson` (ε = 0.5) | `A = 1 − [(1/n)Σ (xᵢ/μ)^(1−ε)]^(1/(1−ε))` | weights the poorest more than Gini |
| `nash` | deficit `1 − GM/mean` | GM = geometric mean (Nash SW); API also surfaces raw GM as `metric_value` |

The metric is an **instance-level choice**: `WELFARE_METRIC` env at boot, the
TUI session picker, or the `{"metric": …}` body of `POST /api/admin/reset`.
Wherever you see `Gini` in a JSON field name (`gini`, `gini_after`,
`gini_target`) it holds *this* index for the selected metric — the names are
legacy for API stability.

### Mandates (solidarity regime only)

`Mandates()` returns `nil` outside the solidarity regime. Under it, it marks
every agent relative to the mean:

- **Contributor** (> +10%) → *give*: sell inventory from the largest holding,
  priced at the current **bid** so it crosses immediately (the concession).
- **Beneficiary** (< −10%) → *receive*: buy at the **ask**, sized to free cash.
- **Neutral** → no suggestion.

### The sim tick (every 1 s)

```
SimTick() — engine lock held
  1. Random-walk each fair value: drift U(−0.0015, +0.002)
     + fat-tailed shock (cubed uniform noise), clamped
  2. Requote the market maker: cancel all its quotes, post 3 bid +
     3 ask levels around fair with spread max(fair×0.0015, $0.01)
     and random sizes 20–89 (cancel-then-repost keeps books bounded).
     Cancel the second system agent's quotes too.
  3. The second system agent, per regime:
       neutral    → quoteDepthTick(): rest 3 levels a side at 5/7/9x the
                    MM spread, size 200–499. Never crosses; adds depth for
                    agents to trade against without steering price.
       solidarity → redistributeTick(): if inequality > GiniTarget,
                    execute its giving mandate as a solidarity order
  4. Advance tournaments: countdown ticks → finalize + queue scores
  5. Snapshot welfare (metric, metric_value, mean, total) → pending
  + chat: the market maker calls out sharp ticks (>1.5%), the solidarity
    bot reports each gift it lands, tournaments announce start/finish
```

Note what is *not* in this list: nothing in the backend takes liquidity on a
view. Both system agents post and cancel; neither crosses the spread to express
an opinion (the solidarity bot's giving order crosses, but it is executing a
published mandate, not trading a signal). Every aggressive order on a neutral
venue came from an agent that decided to send it.

---

## 5. The WebSocket live feed

The hub's job: **one marshaled frame per subscribed symbol per tick**, fanned
out to every client watching that symbol — O(symbols + desks) work, not
O(clients).

```
Ticker (1 s)    Hub.loop (ws.go)           lockedExchange          Client A (NOVA)     Client B (HELX)
     | tick            |                          |                       |                   |
     |---------------->| broadcastTick(seq, ext)  |                       |                   |
     |                 | group clients by symbol  |                       |                   |
     |                 | ex.rlock()               |                       |                   |
     |                 |------------------------->| BuildBaseFrame(sym):   |                   |
     |                 |  <-- one frame per sym --| welfare, stocks, book, |                   |
     |                 |  marshal OUTSIDE the lock| tape, agents, chat,    |                   |
     |                 |  ex.runlock()            | history, tournament    |                   |
     |                 | send(frame_NOVA) -------------------------------->|  render          |
     |                 | send(frame_HELX) ------------------------------------------------->|  render
```

- **Frames** include welfare, stocks, the subscribed symbol's book, the last
  40 trades, agents, chat (≤ 30), the active tournament, and history. Every
  3rd frame (`extendedEvery = 3`) also carries `mandates`. Desk clients (those
  that subscribed with `agent_id`) get their own frame with a `desk` panel.
- **Slow consumers never stall the feed.** Each client has a bounded send
  channel (16 frames); a full channel means the frame is dropped for that
  client, not queued.
- **Client protocol**: `{"type":"subscribe","symbol":…,"agent_id":…}` →
  `{"type":"subscribed",…}` ack; `{"type":"ping"}` → `{"type":"pong"}`.
- **The TUI** dials `/api/ws?symbol=NOVA`, reseeds the market via
  `POST /api/admin/reset` if the server's metric differs from the session
  pick, and reconnects every 2 s on failure while the session goroutine keeps
  handling announce POSTs.

---

## 6. Persistence and the background writer

```
Write path (every mutation)                     Flusher goroutine
  ex.lock()
    ... mutate exchange, filling Pending ...
  pending := ex.DrainPending()
  ex.unlock()
  srv.submitFlush(&pending) ──────────►  ch: flushJob{pending, done?}
                                          loop:
                                            flush(ctx, db, pending)   // one tx
                                            agents/positions/orders   // upserts
                                            trades                    // insert-or-skip
                                            snapshots                 // metric-tagged
                                            finalized tournaments     // scores
```

- **One transaction per batch**, all upserts idempotent (`ON CONFLICT DO
  UPDATE`), so replaying a batch is safe.
- `drain()` is a barrier job used by `POST /api/admin/reset`: the writer
  drains everything queued *before* the tables are wiped, so a stale batch
  can't resurrect old rows.
- **Chat is the exception**: `postChat` writes only to memory (capped at 200)
  and never enters `Pending`. It is conversation, not ledger.

### Boot restore

`Restore(state)` rebuilds the exchange from Postgres:

- stocks, agent caches, positions;
- open orders replayed into books **sorted by id** (preserving time priority);
- human cash/share reservations **recomputed** from resting orders (stored
  `reserved_*` columns are informational only);
- system-bot resting quotes are skipped — they requote on the first tick;
- order-id sequencing resumes from `MAX(id) + 1`;
- running tournaments are reloaded and keep counting down.

---

## 7. The chatroom

Agents write to the floor chat when they act on instructions; the TUI and SDK
monitor it. All messages go through `postChat` (front-pushed, capped at 200,
ephemeral):

| Source | Trigger | Kind |
|---|---|---|
| `RegisterAgent` | an agent joins | `system` |
| solidarity bot | a giving mandate lands fills (sim tick) | `mandate` |
| market maker | a symbol moved > 1.5% in a tick | `market` |
| tournaments | start / finish | `system` |
| any agent | `POST /api/chat {agent_id, text}` (`client.say`) | `chat` |
| floor | `POST /api/admin/announce {text}` (TUI `a`) | `system` + bot replies |

`Announce` broadcasts the instruction and runs a small scripted reply table
(`botReplies`): "give/help" → the solidarity bot offers to redistribute,
"panic/volatile" → the market maker widens, "hello" → greetings. The
definitions are keyword-matched; wiring real behavior to instructions is a
natural next step (see README's ideas list).

---

## 8. Market events (definitions today, engine hooks tomorrow)

`../sdk/events/` holds **definitions only** — one `.md` per scenario with a
machine-readable JSON block embedded in it (fair-value shocks per symbol,
volatility/spread/liquidity multipliers, circuit breakers, solidarity
multipliers, duration/decay). `../sdk/trading_engine/events.py` loads and
validates them (`load_event`, `load_events`), rejecting unknown fields so a
typo can't silently no-op.

The engine does not yet apply them; when it does, the mapping is
straightforward:

| Event field | Engine hook |
|---|---|
| `shock.symbols` / `drift` | SimTick step 1 — fair-value walk override |
| `volatility` | SimTick step 1 — random-walk shock multiplier |
| `spread_multiplier` / `liquidity` | SimTick step 2 — MM requote parameters |
| `circuit_breaker` | new: suspend matching for `halt_ticks` after a `drop_pct` move |
| `solidarity.*` | SimTick step 3 — `GiniTarget` / `GiftRate` multipliers (solidarity regime) |
| `news` | broadcast as `system` chat messages (§7) |

They are, however, already consumed on the **client** side: `trading-desk
--events sdk/events` renders the loaded definitions into the event
strategist's briefing (shock table, headlines, stated mechanism), and that seat
has to work out how much of each shock the tape has already priced in. The
definitions are live input to a decision today, even though the simulation does
not yet fire them.

---

## 9. The agent desk (Python)

The desk lives entirely in `../sdk/trading_engine/` and talks to the backend
through the same public HTTP API as anything else — it gets no privileged
access, and the exchange has no idea an LLM is on the other end.

```
snapshot + agent detail ──► brief.py ──► market brief + desk brief
                                              │
                              ┌───────────────┴───────────────┐
                              ▼   (concurrent, ThreadPool)    ▼
                     agents/analyst.py                agents/strategist.py
                        MarketRead                        MacroRead
                              └───────────────┬───────────────┘
                                              ▼
                                       agents/pm.py  ◄── journal.py recall
                                       PortfolioPlan
                                              ▼
                                      agents/risk.py          judgement
                                      RiskAssessment
                                              ▼
                                      risk.py enforce()       arithmetic
                                      ApprovedTicket[]
                                              ▼
                                     agents/trader.py ──► POST /api/orders
                                     tool-calling loop
                                              ▼
                                       journal.py (JSONL)
```

Three structural choices carry most of the weight:

1. **Typed seams.** Every hand-off is a pydantic model passed as a
   structured-output format, so a drifting agent fails at the boundary rather
   than three steps later. It is also what makes the whole pipeline testable
   with a stub LLM and no network.
2. **Authorization in tools, not prompts.** `agents/trader.py` builds its tools
   per cycle, closed over an authorization session holding the approved
   tickets. A call for an unapproved symbol, a flipped side, or an excess
   quantity returns an error *string* — the model reads it and corrects, and
   nothing reaches the exchange.
3. **Two-layer risk with a fixed order.** The LLM risk officer catches what no
   static rule would (a ticket citing a signal the analyst never produced);
   `risk.enforce()` catches the risk officer. Arithmetic runs last and can only
   reduce.

**Prompt caching** is a design constraint, not an optimisation. Every agent's
system prompt is `[venue_manual (cache breakpoint), role charter]`, and the
manual is byte-identical across all five seats and every cycle, so they share
one cached prefix. `brief.py` therefore contains no wall clock — frames are
identified by sequence number — and there is a test asserting it. The `Ledger`
reports the realised `cache_read_input_tokens` share at the end of every run,
because caching you don't measure is caching you don't have.
