import { api } from './api'
import type { LiveFrame, Snapshot } from './types'

export type LiveMode = 'connecting' | 'ws' | 'polling'

export interface Subscription {
  symbol: string
  agentId: string | null
}

const MAX_WS_FAILURES = 4
const PROBE_MS = 10_000

/**
 * Live market feed: WebSocket first (`/api/ws`), automatic polling fallback,
 * and periodic re-probing so a recovered backend upgrades us back to WS.
 *
 * Returns a dispose function.
 */
export function connectLive(
  getSub: () => Subscription,
  onFrame: (f: LiveFrame) => void,
  onStatus: (mode: LiveMode) => void,
): () => void {
  let ws: WebSocket | null = null
  let disposed = false
  let failures = 0
  let pollTimer: number | undefined
  let probeTimer: number | undefined
  let seq = 0
  let lastSub: Subscription | null = null

  const wsUrl = `${location.protocol === 'https:' ? 'wss' : 'ws'}://${location.host}/api/ws`

  const sendSub = (sub: Subscription) => {
    if (ws?.readyState === WebSocket.OPEN) {
      ws.send(JSON.stringify({ type: 'subscribe', symbol: sub.symbol, agent_id: sub.agentId }))
      lastSub = sub
    }
  }

  // --- polling fallback -----------------------------------------------------
  const pollOnce = async (): Promise<LiveFrame | null> => {
    const sub = getSub()
    try {
      const snap: Snapshot = await api.snapshot(sub.symbol)
      const w = await api.welfare().catch(() => null)
      const desk = sub.agentId ? await api.agent(sub.agentId).catch(() => null) : null
      seq += 1
      return {
        type: 'snapshot',
        seq,
        stocks: snap.stocks,
        book: snap.book,
        tape: snap.tape,
        agents: snap.agents,
        welfare: snap.welfare,
        tournament: snap.tournament ?? null,
        mandates: w?.agents,
        history: w?.history ?? [],
        desk: desk ?? undefined,
      }
    } catch {
      return null
    }
  }

  const startPolling = () => {
    if (pollTimer !== undefined || disposed) return
    onStatus('polling')
    pollTimer = window.setInterval(async () => {
      const f = await pollOnce()
      if (f) onFrame(f)
    }, 1200)
    void pollOnce().then((f) => f && onFrame(f))
  }

  const stopPolling = () => {
    if (pollTimer !== undefined) {
      window.clearInterval(pollTimer)
      pollTimer = undefined
    }
  }

  // --- websocket ------------------------------------------------------------
  const connect = () => {
    if (disposed) return
    onStatus(failures >= MAX_WS_FAILURES ? 'polling' : 'connecting')
    try {
      ws = new WebSocket(wsUrl)
    } catch {
      scheduleReconnect()
      return
    }

    ws.onopen = () => {
      failures = 0
      stopPolling()
      onStatus('ws')
      sendSub(getSub())
    }

    ws.onmessage = (ev) => {
      try {
        const msg = JSON.parse(ev.data as string)
        if (msg?.type === 'snapshot') onFrame(msg as LiveFrame)
      } catch {
        /* ignore malformed frames */
      }
    }

    ws.onclose = () => {
      ws = null
      if (!disposed) scheduleReconnect()
    }

    ws.onerror = () => ws?.close()
  }

  const scheduleReconnect = () => {
    failures += 1
    if (failures === MAX_WS_FAILURES) startPolling()
    if (failures > MAX_WS_FAILURES * 3) {
      // Keep polling; probe occasionally for a revived socket.
      if (probeTimer === undefined && !disposed) {
        probeTimer = window.setInterval(() => {
          stopPolling()
          failures = 0
          probeTimer = undefined
          connect()
        }, PROBE_MS)
      }
      return
    }
    window.setTimeout(connect, Math.min(1500 * failures, 6000))
  }

  connect()

  // React to subscription changes.
  const subTimer = window.setInterval(() => {
    const sub = getSub()
    if (lastSub && (lastSub.symbol !== sub.symbol || lastSub.agentId !== sub.agentId)) {
      sendSub(sub)
    } else if (!lastSub) {
      lastSub = sub
    }
  }, 500)

  return () => {
    disposed = true
    window.clearInterval(subTimer)
    if (probeTimer !== undefined) window.clearInterval(probeTimer)
    stopPolling()
    ws?.close()
    ws = null
  }
}
