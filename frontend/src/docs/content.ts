export interface EndpointParam {
  name: string
  location: 'body' | 'query' | 'path'
  type: string
  required?: boolean
  description: string
}

export interface EndpointDoc {
  method: 'GET' | 'POST' | 'DELETE'
  path: string
  summary: string
  details?: string[]
  params?: EndpointParam[]
  request?: string
  response?: string
  /** GET endpoints can be executed straight from this page. */
  tryIt?: string
}

export const ENDPOINTS: EndpointDoc[] = [
  {
    method: 'GET',
    path: '/api/health',
    summary: 'Liveness probe. Reports whether Postgres is reachable.',
    response: `{
  "status": "ok",
  "database": "connected"
}`,
    tryIt: '/api/health',
  },
  {
    method: 'GET',
    path: '/api/snapshot?symbol=NOVA',
    summary:
      'One-shot aggregate for UI polling: welfare stats, all listings, the book for one symbol, recent tape, agent leaderboard and the active tournament.',
    params: [
      { name: 'symbol', location: 'query', type: 'string', description: 'symbol whose order book is included (default NOVA)' },
    ],
    tryIt: '/api/snapshot?symbol=NOVA',
  },
  {
    method: 'GET',
    path: '/api/welfare',
    summary:
      'Collective-welfare report: Gini coefficient vs target, mean equity, a mandate (role + suggested trade) for every agent, and the recent Gini history.',
    response: `{
  "welfare": { "gini": 0.412, "mean_equity": 250000, "gini_target": 0.2 },
  "agents": [
    {
      "agent_id": "…", "name": "kropotkin",
      "role": "beneficiary", "deviation": -0.31,
      "suggestion": {
        "symbol": "NOVA", "side": "buy", "qty": 54,
        "limit": 184.11,
        "rationale": "You are -31.2% below the mean…"
      }
    }
  ],
  "history": [ { "gini": 0.41, "ts": "…" } ]
}`,
    tryIt: '/api/welfare',
  },
  {
    method: 'GET',
    path: '/api/stocks',
    summary: 'All listed symbols with last trade, best bid/ask and change vs previous close.',
    tryIt: '/api/stocks',
  },
  {
    method: 'GET',
    path: '/api/book/{symbol}?levels=10',
    summary: 'Aggregated depth ladder for one symbol, best prices first.',
    params: [
      { name: 'symbol', location: 'path', type: 'string', required: true, description: 'e.g. NOVA' },
      { name: 'levels', location: 'query', type: 'int ≤50', description: 'depth levels per side (default 10)' },
    ],
    tryIt: '/api/book/NOVA?levels=5',
  },
  {
    method: 'GET',
    path: '/api/trades?limit=50&symbol=',
    summary: 'Recent tape, newest first. Each print carries buyer/seller equity and post-trade Gini.',
    params: [
      { name: 'limit', location: 'query', type: 'int ≤400', description: 'max prints' },
      { name: 'symbol', location: 'query', type: 'string', description: 'filter by symbol' },
    ],
    tryIt: '/api/trades?limit=10',
  },
  {
    method: 'POST',
    path: '/api/agents',
    summary: 'Register a new agent with $100,000 play money.',
    request: `{ "name": "kropotkin" }`,
    response: `{ "agent_id": "7f3c…", "name": "kropotkin", "starting_cash": 100000.0 }`,
  },
  {
    method: 'GET',
    path: '/api/agents',
    summary: 'Leaderboard sorted by equity with computed role per agent.',
    tryIt: '/api/agents',
  },
  {
    method: 'GET',
    path: '/api/agents/{id}',
    summary: 'Full desk view: cash/reserved/free, equity, role, current mandate, positions and open orders.',
    tryIt: undefined,
  },
  {
    method: 'POST',
    path: '/api/orders',
    summary:
      'Place an order. Limit orders rest until crossed; market orders are immediate-or-cancel. Cash/shares are reserved up-front so you cannot over-commit.',
    params: [
      { name: 'agent_id', location: 'body', type: 'uuid', required: true, description: 'who is trading' },
      { name: 'symbol', location: 'body', type: 'string', required: true, description: 'listing symbol' },
      { name: 'side', location: 'body', type: '"buy" | "sell"', required: true, description: '' },
      { name: 'kind', location: 'body', type: '"limit" | "market"', description: 'default limit' },
      { name: 'qty', location: 'body', type: 'int > 0', required: true, description: 'shares' },
      { name: 'price', location: 'body', type: 'number > 0', description: 'required when kind=limit' },
    ],
    request: `{
  "agent_id": "7f3c…", "symbol": "NOVA", "side": "buy",
  "kind": "limit", "qty": 10, "price": 184.50
}`,
    response: `{
  "order":   { "id": 42, "status": "partially_filled", "filled": 6, … },
  "fills":   [ { "trade_id": "…", "price": 184.11, "qty": 6 } ],
  "free_cash": 98905.34
}`,
  },
  {
    method: 'DELETE',
    path: '/api/orders/{id}?agent_id=',
    summary: 'Cancel one of your resting orders; releases its reservation.',
    params: [
      { name: 'id', location: 'path', type: 'int', required: true, description: 'order id' },
      { name: 'agent_id', location: 'query', type: 'uuid', required: true, description: 'must own the order' },
    ],
  },
  {
    method: 'POST',
    path: '/api/tournaments',
    summary: 'Create a tournament session. Entries join while it is open.',
    request: `{ "name": "welfare-games", "duration_ticks": 90 }`,
    response: `{ "id": "…", "status": "open", "entries": [], … }`,
  },
  {
    method: 'GET',
    path: '/api/tournaments',
    summary: 'All tournaments, running first. Entries include live score previews.',
    tryIt: '/api/tournaments',
  },
  {
    method: 'GET',
    path: '/api/tournaments/{id}',
    summary: 'One tournament with leaderboard. While running, scores are previews; after finish they are final.',
    tryIt: '/api/tournaments',
  },
  {
    method: 'POST',
    path: '/api/tournaments/{id}/enter',
    summary: 'Enroll an agent under a strategy label (only while status=open).',
    request: `{ "agent_id": "7f3c…", "strategy": "mandate-follower" }`,
  },
  {
    method: 'POST',
    path: '/api/tournaments/{id}/start',
    summary:
      'Begin the competition: baseline equities are captured, then each sim tick (~1 s) decrements the clock. At zero, results are scored and persisted.',
  },
  {
    method: 'POST',
    path: '/api/admin/reset',
    summary: 'Wipe every table and reseed listings + system bots. Fresh market.',
  },
]

export const WS_FRAME_EXAMPLE = `{
  "type": "snapshot",
  "seq": 17,
  "stocks":  [ { "symbol": "NOVA", "bid": 184.1, "ask": 184.25, … } ],
  "book":    { "symbol": "NOVA", "bids": [[184.1, 64]], "asks": [[184.25, 38]] },
  "tape":    [ { "price": 184.11, "qty": 12, "buyer": "…", "seller": "…",
                 "buyer_equity": 92100, "seller_equity": 402000,
                 "gini_after": 0.398, "ts": "…" } ],
  "agents":  [ { "id": "…", "name": "kropotkin", "equity": 100231, "role": "neutral" } ],
  "welfare": { "gini": 0.398, "mean_equity": 250112, "gini_target": 0.2 },
  "tournament": null,

  // only on every 3rd frame:
  "mandates": [ { "agent_id": "…", "role": "contributor", "suggestion": { … } } ],

  // rolling in-memory trend (no DB read):
  "history": [ { "gini": 0.401, "ts": "…" } ],

  // only when subscribed with agent_id:
  "desk": { "cash": 90000, "free_cash": 89000, "positions": […], "open_orders": […], "mandate": { … } }
}`

export const WS_CLIENT_MESSAGES = `// choose what you receive — safe to resend any time:
{ "type": "subscribe", "symbol": "HELX", "agent_id": "7f3c…" }

// application-level keepalive:
{ "type": "ping" }
→ { "type": "pong" }`

export const CODEBASE_GUIDE: { file: string; role: string }[] = [
  {
    file: 'backend/src/engine.rs',
    role: 'The heart: pure, synchronous, database-free. Price-time priority books, reservation accounting, self-trade prevention, welfare/Gini math, mandate generation, need-priority matching for solidarity orders, tournament state & scoring, and the sim_tick() market simulation. Everything here is unit-tested without Postgres.',
  },
  {
    file: 'backend/src/store.rs',
    role: 'SeaORM persistence. connect/migrate/seed on boot; flush(pending) writes one mutation batch as a single transaction of idempotent upserts; boot_exchange rebuilds books from open orders and reloads running tournaments; save_tournament upserts competition rows.',
  },
  {
    file: 'backend/src/api.rs',
    role: 'HTTP layer. Thin handlers that lock the exchange, mutate, drain the pending buffer, unlock, then await the DB write. Maps engine errors to JSON responses.',
  },
  {
    file: 'backend/src/ws.rs',
    role: 'WebSocket endpoint /api/ws. Two tasks per connection: a sender pushing LiveFrames every second and a receiver handling subscribe/ping messages. Subscription preferences are shared via a tiny Arc<Prefs>.',
  },
  {
    file: 'backend/src/views.rs',
    role: 'Read-models shared by REST and WS: leaderboards, desk views, and build_frame() which assembles the LiveFrame payload.',
  },
  {
    file: 'backend/src/entities/',
    role: 'Hand-written SeaORM entity structs — one per table (agents, stocks, orders, trades, positions, welfare_snapshots, tournaments, tournament_entries).',
  },
  {
    file: 'backend/migration/',
    role: 'SeaORM migrations. Two so far: core schema (m1) and tournament tables (m2). Applied automatically at startup.',
  },
  {
    file: 'frontend/src/live.ts',
    role: 'Live feed client: connects to /api/ws, resubscribes when symbol/agent changes, falls back to polling after repeated failures and re-probes to upgrade back to WS.',
  },
  {
    file: 'frontend/src/App.tsx',
    role: 'Dashboard shell. Routes between the trading floor and this docs page; feeds every panel from LiveFrames instead of polling.',
  },
  {
    file: 'sdk/python/ · sdk/typescript/',
    role: 'Agent SDKs with identical concepts: TradingClient (REST), WatchStream (live frames), Strategy base class, MandateStrategy reference implementation, tournament helpers and CLI examples.',
  },
]

export const DATA_FLOW = `place_order (HTTP POST)
      │
      ▼
┌──────────────────────────── Exchange (behind one mutex) ───────────────────────┐
│ validate → reserve cash/shares                                                 │
│ match loop: pick best counterparty (need-priority if solidarity)               │
│   ├─ settle: buyer.cash−=cost · seller.cash+=cost · positions move             │
│   ├─ trade record (+equities+gini) → tape → pending.trades                     │
│   └─ maker order record updated (filled/status)                                │
│ remainder: limit → rests · market → cancelled                                  │
│ release unused reservation                                                     │
│ tournaments: attribute volume/prosocial fills to enrolled agents               │
└────────────────────────────────────────────────────────────────────────────────┘
      │ drain_pending()                       ▲ every 1s: sim_tick()
      ▼                                       │ requote MM · solidarity flow
store::flush → ONE transaction of upserts ────┘ countdown tournaments · snapshot gini
      │                                        └→ pending.snapshots/history
      ▼
   Postgres ◄── boot_exchange rebuilds books/accounts/tournaments on restart`
