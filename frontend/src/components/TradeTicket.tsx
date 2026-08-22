import { useState } from 'react'
import type { AgentDetail, Mandate, OrderKind, Side, StockView } from '../types'
import { api } from '../api'
import { money, usd } from '../format'

export function TradeTicket({
  stocks,
  agentId,
  agentDetail,
  onDone,
}: {
  stocks: StockView[]
  agentId: string | null
  agentDetail: AgentDetail | null
  onDone: (msg: string, ok: boolean) => void
}) {
  const [symbol, setSymbol] = useState(stocks[0]?.symbol ?? 'NOVA')
  const [side, setSide] = useState<Side>('buy')
  const [kind, setKind] = useState<OrderKind>('limit')
  const [qty, setQty] = useState('10')
  const [price, setPrice] = useState('')
  const [rationale, setRationale] = useState<string | null>(null)
  const [busy, setBusy] = useState(false)

  const mandate: Mandate | undefined = agentDetail?.mandate

  const applyMandate = () => {
    if (!mandate?.suggestion) {
      setRationale('No active mandate — you are within the solidarity band. Trade freely.')
      return
    }
    const s = mandate.suggestion
    setSymbol(s.symbol)
    setSide(s.side)
    setKind('limit')
    setQty(String(s.qty))
    setPrice(String(s.limit))
    setRationale(s.rationale)
  }

  const submit = async (e: React.FormEvent) => {
    e.preventDefault()
    if (!agentId) return
    const q = Number(qty)
    if (!Number.isFinite(q) || q <= 0) return onDone('Quantity must be a positive number.', false)
    const p = Number(price)
    if (kind === 'limit' && (!Number.isFinite(p) || p <= 0))
      return onDone('Limit price must be a positive number.', false)
    setBusy(true)
    try {
      const res = await api.placeOrder({
        agent_id: agentId,
        symbol,
        side,
        kind,
        qty: q,
        price: kind === 'limit' ? p : undefined,
      })
      const filled = res.fills.reduce((acc, f) => acc + f.qty, 0)
      onDone(
        filled > 0
          ? `Filled ${filled}/${res.order.qty} ${symbol} · avg ${money(
              res.fills.reduce((a, f) => a + f.price * f.qty, 0) / filled,
            )} · free cash ${usd(res.free_cash)}`
          : `${side} ${res.order.qty} ${symbol} resting (${res.order.status})`,
        true,
      )
    } catch (err) {
      onDone(err instanceof Error ? err.message : String(err), false)
    } finally {
      setBusy(false)
    }
  }

  return (
    <section className="panel">
      <div className="panel-head">
        <h2>Trade ticket</h2>
        <button className="btn ghost" type="button" onClick={applyMandate} disabled={!agentId}>
          ✊ Follow my mandate
        </button>
      </div>
      {rationale && <p className="rationale">{rationale}</p>}
      <form onSubmit={submit} className="ticket">
        <label>
          Symbol
          <select value={symbol} onChange={(e) => setSymbol(e.target.value)}>
            {stocks.map((s) => (
              <option key={s.symbol} value={s.symbol}>
                {s.symbol}
              </option>
            ))}
          </select>
        </label>
        <div className="seg">
          <button type="button" className={side === 'buy' ? 'on buy' : ''} onClick={() => setSide('buy')}>
            Buy
          </button>
          <button type="button" className={side === 'sell' ? 'on sell' : ''} onClick={() => setSide('sell')}>
            Sell
          </button>
        </div>
        <div className="seg">
          <button type="button" className={kind === 'limit' ? 'on' : ''} onClick={() => setKind('limit')}>
            Limit
          </button>
          <button type="button" className={kind === 'market' ? 'on' : ''} onClick={() => setKind('market')}>
            Market
          </button>
        </div>
        <label>
          Qty
          <input type="number" min="1" value={qty} onChange={(e) => setQty(e.target.value)} />
        </label>
        <label>
          Price
          <input
            type="number"
            min="0.01"
            step="0.01"
            value={price}
            placeholder={kind === 'market' ? 'market' : '0.00'}
            disabled={kind === 'market'}
            onChange={(e) => setPrice(e.target.value)}
          />
        </label>
        <button className="btn primary" type="submit" disabled={!agentId || busy}>
          {busy ? 'Working…' : `Place ${side}`}
        </button>
      </form>
    </section>
  )
}
