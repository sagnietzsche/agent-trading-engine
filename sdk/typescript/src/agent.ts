import type { TradingClient } from './client.js'
import { WatchStream } from './ws.js'
import type { Context, OrderIntent, Strategy } from './strategies.js'
import type { LiveFrame } from './types.js'

export interface RunOptions {
  durationMs?: number
  symbol?: string
  /** Poll REST instead of WebSocket (default false). */
  useWs?: boolean
  log?: (msg: string) => void
}

export interface RunStats {
  ticks: number
  orders: number
  rejected: number
}

/** Binds an agent id to a client and runs strategies against live frames. */
export class Agent {
  ordersSent = 0

  private constructor(
    readonly client: TradingClient,
    readonly agentId: string,
    readonly name: string,
  ) {}

  static async create(client: TradingClient, name: string): Promise<Agent> {
    const created = await client.createAgent(name)
    return new Agent(client, created.agent_id, name)
  }

  submit(intent: OrderIntent) {
    return this.client.placeOrder({
      agent_id: this.agentId,
      symbol: intent.symbol,
      side: intent.side,
      kind: intent.kind,
      qty: intent.qty,
      price: intent.price,
    })
  }

  async run(strategy: Strategy, opts: RunOptions = {}): Promise<RunStats> {
    const {
      durationMs = 60_000,
      symbol = 'NOVA',
      useWs = true,
      log = (m: string) => console.log(m),
    } = opts
    const deadline = Date.now() + durationMs
    const stats: RunStats = { ticks: 0, orders: 0, rejected: 0 }

    const handleFrame = async (frame: LiveFrame): Promise<void> => {
      if (Date.now() > deadline) return
      const desk = await this.client.agent(this.agentId)
      const mandates = frame.mandates ?? []
      const mandate =
        mandates.find((m) => m.agent_id === this.agentId) ?? desk.mandate ?? undefined

      let queued: OrderIntent[] = []
      const ctx: Context = {
        client: this.client,
        agentId: this.agentId,
        frame,
        desk,
        welfare: frame.welfare,
        mandate,
        tournament: frame.tournament ?? null,
        submit: (intent) => queued.push(intent),
      }
      const returned = (strategy.onTick(ctx) as OrderIntent[] | void) ?? []
      for (const intent of [...queued, ...returned]) {
        try {
          await this.submit(intent)
          stats.orders += 1
          this.ordersSent += 1
        } catch (err) {
          stats.rejected += 1
          log(`  order rejected: ${err instanceof Error ? err.message : String(err)}`)
        }
      }
      stats.ticks += 1
      const t = frame.tournament
      log(
        `[${strategy.name}] tick=${stats.ticks} equity=$${desk.equity.toLocaleString('en-US', { maximumFractionDigits: 0 })} role=${mandate?.role ?? '?'} orders=${stats.orders}` +
          (t ? ` | ${t.name}:${t.status}(${t.ticks_left}t)` : ''),
      )
    }

    let stream: WatchStream | null = null
    try {
      if (useWs) {
        stream = new WatchStream(this.client.base, {
          symbol,
          agentId: this.agentId,
          onFrame: (f) => void handleFrame(f),
        }).start()
        while (Date.now() < deadline) await sleep(250)
      } else {
        while (Date.now() < deadline) {
          const snap = await this.client.snapshot(symbol)
          const w = await this.client.welfare().catch(() => ({ agents: [] as never[] }))
          await handleFrame({ ...snap, type: 'snapshot', mandates: w.agents })
          await sleep(1200)
        }
      }
    } finally {
      stream?.close()
    }
    return stats
  }
}

const sleep = (ms: number) => new Promise((r) => setTimeout(r, ms))
