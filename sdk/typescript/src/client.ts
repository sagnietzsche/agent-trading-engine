import type {
  AgentDetail,
  AgentSummary,
  BookView,
  LiveFrame,
  OrderKind,
  OrderRecord,
  Side,
  StockView,
  TournamentView,
  Trade,
  Welfare,
} from './types.js'

export class TradingError extends Error {}

async function req<T>(base: string, method: string, path: string, body?: unknown): Promise<T> {
  const res = await fetch(`${base}${path}`, {
    method,
    headers: { 'Content-Type': 'application/json' },
    body: body === undefined ? undefined : JSON.stringify(body),
  })
  const json = (await res.json().catch(() => ({}))) as Record<string, unknown>
  if (!res.ok) throw new TradingError(String(json.error ?? `${res.status} ${res.statusText}`))
  return json as T
}

export class TradingClient {
  readonly base: string

  constructor(base = 'http://127.0.0.1:8080') {
    this.base = base.replace(/\/$/, '')
  }

  health(): Promise<{ status: string; database: string }> {
    return req(this.base, 'GET', '/api/health')
  }
  snapshot(symbol = 'NOVA'): Promise<{
    welfare: Welfare
    stocks: StockView[]
    book: BookView | null
    tape: Trade[]
    agents: AgentSummary[]
    tournament: TournamentView | null
  }> {
    return req(this.base, 'GET', `/api/snapshot?symbol=${encodeURIComponent(symbol)}`)
  }
  welfare(): Promise<{
    welfare: Welfare
    agents: import('./types.js').Mandate[]
    history: import('./types.js').WelfarePoint[]
  }> {
    return req(this.base, 'GET', '/api/welfare')
  }
  stocks(): Promise<StockView[]> {
    return req(this.base, 'GET', '/api/stocks')
  }
  book(symbol: string, levels = 10): Promise<BookView> {
    return req(this.base, 'GET', `/api/book/${symbol}?levels=${levels}`)
  }
  trades(limit = 50, symbol?: string): Promise<Trade[]> {
    const q = new URLSearchParams({ limit: String(limit) })
    if (symbol) q.set('symbol', symbol)
    return req(this.base, 'GET', `/api/trades?${q}`)
  }

  createAgent(name: string): Promise<{ agent_id: string; name: string; starting_cash: number }> {
    return req(this.base, 'POST', '/api/agents', { name })
  }
  agent(id: string): Promise<AgentDetail> {
    return req(this.base, 'GET', `/api/agents/${id}`)
  }
  placeOrder(o: {
    agent_id: string
    symbol: string
    side: Side
    kind?: OrderKind
    qty: number
    price?: number
  }): Promise<{ order: OrderRecord; fills: { trade_id: string; price: number; qty: number }[]; free_cash: number }> {
    return req(this.base, 'POST', '/api/orders', o)
  }
  cancelOrder(orderId: number, agentId: string): Promise<OrderRecord> {
    return req(this.base, 'DELETE', `/api/orders/${orderId}?agent_id=${agentId}`)
  }

  createTournament(name: string, durationTicks = 90): Promise<TournamentView> {
    return req(this.base, 'POST', '/api/tournaments', { name, duration_ticks: durationTicks })
  }
  tournaments(): Promise<TournamentView[]> {
    return req(this.base, 'GET', '/api/tournaments')
  }
  tournament(id: string): Promise<TournamentView> {
    return req(this.base, 'GET', `/api/tournaments/${id}`)
  }
  enterTournament(id: string, agentId: string, strategy = 'custom'): Promise<TournamentView> {
    return req(this.base, 'POST', `/api/tournaments/${id}/enter`, {
      agent_id: agentId,
      strategy,
    })
  }
  startTournament(id: string): Promise<TournamentView> {
    return req(this.base, 'POST', `/api/tournaments/${id}/start`)
  }
  resetMarket(): Promise<{ status: string }> {
    return req(this.base, 'POST', '/api/admin/reset')
  }
}

