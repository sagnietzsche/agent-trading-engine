import type { StockView } from '../types'
import { money, pct } from '../format'

export function StocksTable({
  stocks,
  selected,
  onSelect,
}: {
  stocks: StockView[]
  selected: string
  onSelect: (symbol: string) => void
}) {
  return (
    <section className="panel">
      <h2>Market</h2>
      <table className="tbl">
        <thead>
          <tr>
            <th>Symbol</th>
            <th className="num">Last</th>
            <th className="num">Chg</th>
            <th className="num">Bid</th>
            <th className="num">Ask</th>
          </tr>
        </thead>
        <tbody>
          {stocks.map((s) => {
            const last = s.last_trade ?? s.fair
            const chg = (last - s.prev_close) / s.prev_close
            const cls = chg > 0 ? 'up' : chg < 0 ? 'down' : ''
            return (
              <tr
                key={s.symbol}
                className={s.symbol === selected ? 'sel' : ''}
                onClick={() => onSelect(s.symbol)}
              >
                <td>
                  <b>{s.symbol}</b>
                  <span className="muted name-cell">{s.name}</span>
                </td>
                <td className={`num ${cls}`}>{money(last)}</td>
                <td className={`num ${cls}`}>{pct(chg)}</td>
                <td className="num">{money(s.bid)}</td>
                <td className="num">{money(s.ask)}</td>
              </tr>
            )
          })}
        </tbody>
      </table>
      <p className="muted footnote">Fair values follow a random walk; the market maker keeps spreads tight.</p>
    </section>
  )
}
