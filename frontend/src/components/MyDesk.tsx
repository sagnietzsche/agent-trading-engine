import type { AgentDetail } from '../types'
import { api } from '../api'
import { money, usd } from '../format'

export function MyDesk({
  detail,
  onChanged,
}: {
  detail: AgentDetail | null
  onChanged: () => void
}) {
  if (!detail)
    return (
      <section className="panel">
        <h2>My desk</h2>
        <p className="muted">Select or create an agent to start trading.</p>
      </section>
    )

  const cancel = async (id: number) => {
    try {
      await api.cancelOrder(id, detail.id)
      onChanged()
    } catch (err) {
      alert(err instanceof Error ? err.message : String(err))
    }
  }

  return (
    <section className="panel">
      <div className="panel-head">
        <h2>My desk — {detail.name}</h2>
        <span className={`chip ${detail.role}`}>{detail.role}</span>
      </div>
      <div className="desk-stats">
        <span>Cash <b>{usd(detail.cash)}</b></span>
        <span>Free <b>{usd(detail.free_cash)}</b></span>
        <span>Equity <b>{usd(detail.equity)}</b></span>
      </div>

      {detail.mandate.suggestion && (
        <p className="rationale">📣 {detail.mandate.suggestion.rationale}</p>
      )}

      <h3 className="subhead">Positions</h3>
      {detail.positions.length === 0 ? (
        <p className="muted">No positions yet.</p>
      ) : (
        <table className="tbl compact">
          <thead>
            <tr>
              <th>Sym</th>
              <th className="num">Qty</th>
              <th className="num">Free</th>
              <th className="num">Mark</th>
              <th className="num">Value</th>
            </tr>
          </thead>
          <tbody>
            {detail.positions.map((p) => (
              <tr key={p.symbol}>
                <td>{p.symbol}</td>
                <td className="num">{p.qty}</td>
                <td className="num">{p.free}</td>
                <td className="num">{money(p.mark)}</td>
                <td className="num">{usd(p.value, 0)}</td>
              </tr>
            ))}
          </tbody>
        </table>
      )}

      <h3 className="subhead">Open orders</h3>
      {detail.open_orders.length === 0 ? (
        <p className="muted">No working orders.</p>
      ) : (
        <table className="tbl compact">
          <thead>
            <tr>
              <th>#</th>
              <th>Sym</th>
              <th>Side</th>
              <th className="num">Qty/Filled</th>
              <th className="num">Price</th>
              <th />
            </tr>
          </thead>
          <tbody>
            {detail.open_orders.map((o) => (
              <tr key={o.id}>
                <td className="muted">{o.id}</td>
                <td>{o.symbol}</td>
                <td className={o.side === 'buy' ? 'up' : 'down'}>{o.side}</td>
                <td className="num">
                  {o.qty}/{o.filled}
                </td>
                <td className="num">{o.price == null ? 'MKT' : money(o.price)}</td>
                <td>
                  <button className="btn tiny" onClick={() => cancel(o.id)}>
                    cancel
                  </button>
                </td>
              </tr>
            ))}
          </tbody>
        </table>
      )}
    </section>
  )
}
