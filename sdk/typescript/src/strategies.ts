import type { TradingClient } from './client.js'
import type { AgentDetail, LiveFrame, Mandate, OrderKind, Side, TournamentView, Welfare } from './types.js'

export interface OrderIntent {
  symbol: string
  side: Side
  qty: number
  kind: OrderKind
  price?: number
}

/** Everything a strategy may look at for one tick. */
export interface Context {
  client: TradingClient
  agentId: string
  frame: LiveFrame
  desk: AgentDetail
  welfare: Welfare | undefined
  mandate: Mandate | undefined
  tournament: TournamentView | null | undefined

  /** Queue an order to be submitted after onTick returns. */
  submit(intent: OrderIntent): void
}

/**
 * Implement `onTick`; return intents or queue them via ctx.submit().
 * Thrown errors / rejected orders are logged by the runner and skipped.
 */
export interface Strategy {
  readonly name: string
  onTick(ctx: Context): OrderIntent[] | void
}

/** The cooperative reference bot: obey the welfare mandate verbatim. */
export class MandateStrategy implements Strategy {
  readonly name = 'mandate'

  onTick(ctx: Context): OrderIntent[] {
    const s = ctx.mandate?.suggestion
    if (!s) return []
    return [{ symbol: s.symbol, side: s.side, qty: s.qty, kind: 'limit', price: s.limit }]
  }
}

/**
 * The foil: chase momentum, never read the mandate.
 * Market-buys dips (>0.5% below prev close), market-sells rips it holds.
 */
export class GreedyMomentumStrategy implements Strategy {
  readonly name = 'greedy'
  constructor(private readonly clipQty = 10) {}

  onTick(ctx: Context): OrderIntent[] {
    const intents: OrderIntent[] = []
    let freeCash = ctx.desk.free_cash
    const positions = new Map(ctx.desk.positions.map((p) => [p.symbol, p]))
    for (const stock of ctx.frame.stocks ?? []) {
      if (stock.last_trade == null) continue
      const change = stock.last_trade / stock.prev_close - 1
      const pos = positions.get(stock.symbol)
      const held = pos ? Math.max(0, pos.free) : 0
      if (change < -0.005 && freeCash > stock.last_trade * this.clipQty) {
        intents.push({ symbol: stock.symbol, side: 'buy', qty: this.clipQty, kind: 'market' })
        freeCash -= stock.last_trade * this.clipQty
      } else if (change > 0.005 && held > 0) {
        intents.push({
          symbol: stock.symbol,
          side: 'sell',
          qty: Math.min(held, this.clipQty),
          kind: 'market',
        })
      }
    }
    return intents
  }
}
