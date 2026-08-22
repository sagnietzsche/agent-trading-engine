import type { Trade } from '../types'
import { clockOf, money, shortId, usd } from '../format'

export function TapePanel({ tape }: { tape: Trade[] }) {
  return (
    <section className="panel">
      <h2>Time &amp; sales</h2>
      <table className="tbl compact">
        <thead>
          <tr>
            <th>Time</th>
            <th>Sym</th>
            <th className="num">Price</th>
            <th className="num">Qty</th>
            <th>Buyer/Seller</th>
          </tr>
        </thead>
        <tbody>
          {tape.map((t) => {
            const gap = t.buyer_equity - t.seller_equity
            const dir = gap > 0 ? 'down' : gap < 0 ? 'up' : ''
            return (
              <tr key={t.id}>
                <td className="muted">{clockOf(t.ts)}</td>
                <td>{t.symbol}</td>
                <td className="num">{money(t.price)}</td>
                <td className="num">{t.qty}</td>
                <td className={`dir ${dir}`} title={`buyer ${shortId(t.buyer)} · seller ${shortId(t.seller)} · gini ${t.gini_after.toFixed(3)}`}>
                  {shortId(t.buyer)} → {shortId(t.seller)}
                  <span className="muted"> ({usd(gap, 0)})</span>
                </td>
              </tr>
            )
          })}
        </tbody>
      </table>
      <p className="muted footnote">
        Green: wealth moved to a poorer agent. Red: wealth moved to a richer agent.
      </p>
    </section>
  )
}
