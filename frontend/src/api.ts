import type {
  AgentDetail,
  OrderKind,
  PlaceOrderResult,
  Side,
  Snapshot,
  WelfareResp,
} from './types'

async function req<T>(path: string, init?: RequestInit): Promise<T> {
  const res = await fetch(`/api${path}`, {
    headers: { 'Content-Type': 'application/json' },
    ...init,
  })
  const body = await res.json().catch(() => ({}))
  if (!res.ok) {
    throw new Error((body as { error?: string }).error ?? `${res.status} ${res.statusText}`)
  }
  return body as T
}

export const api = {
  snapshot: (symbol: string) => req<Snapshot>(`/snapshot?symbol=${encodeURIComponent(symbol)}`),
  welfare: () => req<WelfareResp>('/welfare'),
  agent: (id: string) => req<AgentDetail>(`/agents/${id}`),

  createAgent: (name: string) =>
    req<{ agent_id: string; name: string; starting_cash: number }>('/agents', {
      method: 'POST',
      body: JSON.stringify({ name }),
    }),

  placeOrder: (o: {
    agent_id: string
    symbol: string
    side: Side
    kind: OrderKind
    qty: number
    price?: number
  }) =>
    req<PlaceOrderResult>('/orders', {
      method: 'POST',
      body: JSON.stringify(o),
    }),

  cancelOrder: (orderId: number, agentId: string) =>
    req<{ status: string }>(`/orders/${orderId}?agent_id=${agentId}`, { method: 'DELETE' }),

  reset: () => req<{ status: string }>('/admin/reset', { method: 'POST' }),
}
