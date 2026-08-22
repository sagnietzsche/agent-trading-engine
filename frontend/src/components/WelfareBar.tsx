import type { Welfare, WelfarePoint } from '../types'

function Sparkline({ points }: { points: WelfarePoint[] }) {
  if (points.length < 2) return <div className="spark-empty">collecting history…</div>
  const w = 260
  const h = 46
  const vals = points.map((p) => p.gini)
  const min = Math.min(...vals)
  const max = Math.max(...vals)
  const span = max - min || 1
  const step = w / (vals.length - 1)
  const path = vals
    .map((v, i) => `${(i * step).toFixed(1)},${(h - ((v - min) / span) * (h - 6) - 3).toFixed(1)}`)
    .join(' ')
  return (
    <svg className="spark" viewBox={`0 0 ${w} ${h}`} preserveAspectRatio="none">
      <polyline points={path} fill="none" stroke="var(--warn)" strokeWidth="1.5" />
    </svg>
  )
}

export function WelfareBar({
  welfare,
  history,
}: {
  welfare: Welfare
  history: WelfarePoint[]
}) {
  const over = welfare.gini > welfare.gini_target
  return (
    <section className="panel welfare">
      <div className="welfare-stats">
        <div className="welfare-item">
          <span className="label">Gini coefficient</span>
          <span className={`big ${over ? 'bad' : 'good'}`}>{welfare.gini.toFixed(3)}</span>
          <span className="sub">
            {over ? 'above' : 'below'} solidarity target {welfare.gini_target.toFixed(2)}
          </span>
        </div>
        <div className="welfare-item">
          <span className="label">Mean equity</span>
          <span className="big">${welfare.mean_equity.toLocaleString('en-US', { maximumFractionDigits: 0 })}</span>
          <span className="sub">across all agents</span>
        </div>
        <div className="welfare-item spark-wrap">
          <span className="label">Inequality trend</span>
          <Sparkline points={history} />
        </div>
      </div>
      <p className="welfare-note">
        The engine optimizes for the collective: when inequality rises above target,
        surplus agents receive giving mandates and their orders are matched to the
        worst-off members first.
      </p>
    </section>
  )
}
