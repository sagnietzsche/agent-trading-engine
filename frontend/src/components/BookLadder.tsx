import type { BookView } from '../types'
import { money } from '../format'

export function BookLadder({ book }: { book: BookView | null }) {
  if (!book)
    return (
      <section className="panel">
        <h2>Order book</h2>
        <p className="muted">No data.</p>
      </section>
    )

  const asks = book.asks.slice(0, 8).reverse()
  const bids = book.bids.slice(0, 8)
  const maxQty = Math.max(
    ...book.asks.slice(0, 8).map(([, q]) => q),
    ...book.bids.slice(0, 8).map(([, q]) => q),
    1,
  )
  const spread = book.asks.length && book.bids.length ? book.asks[0][0] - book.bids[0][0] : null

  return (
    <section className="panel">
      <div className="panel-head">
        <h2>Order book — {book.symbol}</h2>
        <span className="muted">spread {spread == null ? '—' : money(spread)}</span>
      </div>
      <div className="ladder">
        {asks.map(([p, q]) => (
          <div key={`a${p}`} className="ladder-row ask">
            <span className="ladder-bar" style={{ width: `${(q / maxQty) * 100}%` }} />
            <span className="price">{money(p)}</span>
            <span className="qty">{q}</span>
          </div>
        ))}
        <div className="ladder-mid" />
        {bids.map(([p, q]) => (
          <div key={`b${p}`} className="ladder-row bid">
            <span className="ladder-bar" style={{ width: `${(q / maxQty) * 100}%` }} />
            <span className="price">{money(p)}</span>
            <span className="qty">{q}</span>
          </div>
        ))}
      </div>
      <p className="muted footnote">Solidarity orders skip the queue and fill the worst-off members first.</p>
    </section>
  )
}
