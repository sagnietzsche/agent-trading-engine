import type { LiveFrame } from './types.js'

export type LiveStatus = 'connecting' | 'open' | 'closed' | 'error' | 'reconnecting'

export interface WatchOptions {
  symbol?: string
  agentId?: string | null
  onFrame: (frame: LiveFrame) => void
  onStatus?: (status: LiveStatus) => void
}

/**
 * Live market frames from /api/ws with automatic reconnect + resubscribe.
 * Uses Node's global WebSocket (>= v22) or the browser's.
 */
export class WatchStream {
  private ws: WebSocket | null = null
  private closing = false

  constructor(
    private readonly base: string,
    private readonly opts: WatchOptions,
  ) {}

  start(): this {
    this.connect()
    return this
  }

  updateSubscription(symbol?: string, agentId?: string | null): void {
    if (symbol !== undefined) this.opts.symbol = symbol
    if (agentId !== undefined) this.opts.agentId = agentId
    this.send({
      type: 'subscribe',
      symbol: this.opts.symbol ?? 'NOVA',
      agent_id: this.opts.agentId ?? null,
    })
  }

  ping(): void {
    this.send({ type: 'ping' })
  }

  close(): void {
    this.closing = true
    this.ws?.close()
    this.ws = null
  }

  // -- internals ------------------------------------------------------------

  private url(): string {
    const b = this.base.replace(/\/$/, '')
    return `${b.startsWith('https') ? 'wss' : 'ws'}${b.slice(4)}/api/ws`
  }

  private send(msg: unknown): void {
    if (this.ws?.readyState === WebSocket.OPEN) this.ws.send(JSON.stringify(msg))
  }

  private connect(): void {
    this.closing = false
    this.opts.onStatus?.('connecting')
    const ws = new WebSocket(this.url())
    this.ws = ws
    ws.onopen = () => {
      this.opts.onStatus?.('open')
      this.updateSubscription(this.opts.symbol, this.opts.agentId)
    }
    ws.onmessage = (ev) => {
      try {
        const frame = JSON.parse(ev.data as string) as LiveFrame
        if (frame.type === 'snapshot') this.opts.onFrame(frame)
      } catch {
        /* ignore malformed frames */
      }
    }
    ws.onerror = () => this.opts.onStatus?.('error')
    ws.onclose = () => {
      this.opts.onStatus?.('closed')
      if (!this.closing) setTimeout(() => this.connect(), 1500)
    }
  }
}
