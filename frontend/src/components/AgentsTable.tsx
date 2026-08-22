import type { Mandate, WelfareResp } from '../types'
import { pct, usd } from '../format'

export function AgentsTable({
  agents,
  mandates,
  selected,
  onSelect,
}: {
  agents: { id: string; name: string; is_bot: boolean; cash: number; equity: number; deviation: number; role: string }[]
  mandates: WelfareResp['agents']
  selected: string | null
  onSelect: (id: string) => void
}) {
  const mandateOf = (id: string): Mandate | undefined => mandates.find((m) => m.agent_id === id)
  return (
    <section className="panel">
      <h2>Agents</h2>
      <table className="tbl">
        <thead>
          <tr>
            <th>Agent</th>
            <th className="num">Equity</th>
            <th className="num">vs mean</th>
            <th>Role</th>
          </tr>
        </thead>
        <tbody>
          {agents.map((a) => (
            <tr key={a.id} className={a.id === selected ? 'sel' : ''} onClick={() => onSelect(a.id)}>
              <td>
                <b>{a.name}</b>
                {a.is_bot && <span className="chip bot">bot</span>}
              </td>
              <td className="num">{usd(a.equity, 0)}</td>
              <td className={`num ${a.deviation > 0 ? 'up' : a.deviation < 0 ? 'down' : ''}`}>
                {pct(a.deviation)}
              </td>
              <td>
                <span className={`chip ${mandateOf(a.id)?.role ?? a.role}`}>{a.role}</span>
              </td>
            </tr>
          ))}
        </tbody>
      </table>
      <p className="muted footnote">Roles drive the collective objective: contributors give, beneficiaries receive.</p>
    </section>
  )
}
