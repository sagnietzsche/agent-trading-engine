export type Side = 'buy' | 'sell'
export type OrderKind = 'limit' | 'market'
export type Role = 'contributor' | 'beneficiary' | 'neutral'
export type TournamentStatus = 'open' | 'running' | 'finished'

export interface StockView {
  symbol: string
  name: string
  fair: number
  last_trade: number | null
  prev_close: number
  bid: number | null
  ask: number | null
}

export interface BookView {
  symbol: string
  bids: [number, number][]
  asks: [number, number][]
}

export interface Trade {
  id: string
  symbol: string
  price: number
  qty: number
  buyer: string
  seller: string
  buyer_equity: number
  seller_equity: number
  gini_after: number
  ts: string
}

export interface AgentSummary {
  id: string
  name: string
  is_bot: boolean
  cash: number
  equity: number
  deviation: number
  role: Role
}

export interface Suggestion {
  symbol: string
  side: Side
  qty: number
  limit: number
  rationale: string
}

export interface Mandate {
  agent_id: string
  name: string
  equity: number
  deviation: number
  role: Role
  suggestion?: Suggestion
}

export interface Welfare {
  gini: number
  total_equity: number
  mean_equity: number
  gini_target: number
}

export interface WelfarePoint {
  gini: number
  total_equity: number
  mean_equity: number
  ts: string
}

export interface PositionView {
  symbol: string
  qty: number
  reserved: number
  free: number
  mark: number
  value: number
}

export interface OrderRecord {
  id: number
  symbol: string
  side: Side
  kind: OrderKind
  price: number | null
  qty: number
  filled: number
  status: string
  created_at: string
}

export interface AgentDetail {
  id: string
  name: string
  is_bot: boolean
  cash: number
  reserved_cash: number
  free_cash: number
  equity: number
  role: Role
  mandate: Mandate
  positions: PositionView[]
  open_orders: OrderRecord[]
}

export interface TournamentEntryView {
  agent_id: string
  strategy: string
  start_equity: number
  equity_now: number
  return_pct: number
  total_volume: number
  prosocial_volume: number
  coop_share: number
  score: number
}

export interface TournamentView {
  id: string
  name: string
  status: TournamentStatus
  duration_ticks: number
  ticks_left: number
  gini_start: number
  gini_final: number | null
  created_at: string
  started_at: string | null
  finished_at: string | null
  entries: TournamentEntryView[]
}

export interface LiveFrame {
  type: 'snapshot' | 'subscribed' | 'pong'
  seq?: number
  stocks?: StockView[]
  book?: BookView | null
  tape?: Trade[]
  agents?: AgentSummary[]
  welfare?: Welfare
  tournament?: TournamentView | null
  mandates?: Mandate[]
  history?: WelfarePoint[]
  desk?: AgentDetail
}
