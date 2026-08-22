export type Side = 'buy' | 'sell'
export type OrderKind = 'limit' | 'market'
export type Role = 'contributor' | 'beneficiary' | 'neutral'

export interface StockView {
  symbol: string
  name: string
  fair: number
  last_trade: number | null
  prev_close: number
  bid: number | null
  ask: number | null
}

export interface Level {
  0: number // price
  1: number // qty
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

export interface WelfareResp {
  welfare: Welfare
  agents: Mandate[]
  history: WelfarePoint[]
}

export interface Snapshot {
  welfare: Welfare
  stocks: StockView[]
  book: BookView | null
  tape: Trade[]
  agents: AgentSummary[]
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

export interface PlaceOrderResult {
  order: OrderRecord
  fills: { trade_id: string; price: number; qty: number }[]
  free_cash: number
}
