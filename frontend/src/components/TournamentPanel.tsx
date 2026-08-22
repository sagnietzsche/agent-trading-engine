import { useEffect, useState } from 'react'
import { api } from '../api'
import type { TournamentView } from '../types'
import { pct, shortId, usd } from '../format'

export function TournamentPanel({
  tournament,
  selectedAgent,
}: {
  tournament: TournamentView | null
  selectedAgent: string | null
}) {
  const [name, setName] = useState('welfare-games')
  const [duration, setDuration] = useState(90)
  const [strategy, setStrategy] = useState('mandate-follower')
  const [busy, setBusy] = useState(false)
  const [error, setError] = useState<string | null>(null)
  const [past, setPast] = useState<TournamentView[] | null>(null)

  const refresh = () => api.listTournaments().then(setPast).catch(() => setPast(null))
  useEffect(() => {
    void refresh()
  }, [tournament?.id])

  const run = async (fn: () => Promise<unknown>, okMsg?: string) => {
    setBusy(true)
    setError(null)
    try {
      await fn()
      if (okMsg) console.info(okMsg)
    } catch (err) {
      setError(err instanceof Error ? err.message : String(err))
    } finally {
      setBusy(false)
    }
  }

  return (
    <section className="panel">
      <div className="panel-head">
        <h2>Tournament</h2>
        {tournament && <span className={`chip ${tournament.status}`}>{tournament.status}</span>}
      </div>

      {!tournament && (
        <div className="t-create">
          <input placeholder="name" value={name} onChange={(e) => setName(e.target.value)} />
          <input
            type="number"
            min={5}
            max={3600}
            value={duration}
            title="duration in sim ticks (1 tick ≈ 1s)"
            onChange={(e) => setDuration(Number(e.target.value))}
          />
          <button
            className="btn"
            disabled={busy}
            onClick={() =>
              run(() => api.createTournament(name.trim() || 'welfare-games', duration))
            }
          >
            Create tournament
          </button>
        </div>
      )}

      {tournament && (
        <>
          <div className="t-meta">
            <b>{tournament.name}</b>
            {tournament.status === 'running' && (
              <span className="muted">
                {' '}
                · tick {Math.max(0, tournament.duration_ticks - tournament.ticks_left)}/
                {tournament.duration_ticks}
              </span>
            )}
            {tournament.status === 'finished' && tournament.gini_final != null && (
              <span className="muted">
                {' '}
                · gini {tournament.gini_start.toFixed(3)} → {tournament.gini_final.toFixed(3)}
              </span>
            )}
          </div>

          <table className="tbl compact">
            <thead>
              <tr>
                <th>#</th>
                <th>Strategy</th>
                <th>Agent</th>
                <th className="num">Equity</th>
                <th className="num">Ret</th>
                <th className="num">Coop</th>
                <th className="num">Score</th>
              </tr>
            </thead>
            <tbody>
              {tournament.entries.length === 0 && (
                <tr>
                  <td colSpan={7} className="muted">
                    No entries yet.
                  </td>
                </tr>
              )}
              {tournament.entries.map((e, i) => (
                <tr key={e.agent_id} className={e.agent_id === selectedAgent ? 'sel' : ''}>
                  <td className="muted">{i + 1}</td>
                  <td>{e.strategy}</td>
                  <td className="muted">{shortId(e.agent_id)}</td>
                  <td className="num">{usd(e.equity_now, 0)}</td>
                  <td className={`num ${e.return_pct >= 0 ? 'up' : 'down'}`}>{pct(e.return_pct)}</td>
                  <td className="num up">{(e.coop_share * 100).toFixed(0)}%</td>
                  <td className="num">
                    <b>{e.score.toFixed(3)}</b>
                  </td>
                </tr>
              ))}
            </tbody>
          </table>

          <p className="muted footnote">
            score = return + coop_share — a fill counts as cooperative when you are the wealthier
            side trading with a poorer member.
          </p>

          <div className="t-actions">
            {tournament.status === 'open' && (
              <>
                <input
                  placeholder="strategy name"
                  value={strategy}
                  onChange={(e) => setStrategy(e.target.value)}
                />
                <button
                  className="btn ghost"
                  disabled={busy || !selectedAgent}
                  title={selectedAgent ? undefined : 'select an agent first'}
                  onClick={() =>
                    selectedAgent &&
                    run(() => api.enterTournament(tournament.id, selectedAgent, strategy.trim() || 'custom'))
                  }
                >
                  Enter agent
                </button>
                <button className="btn" disabled={busy} onClick={() => run(() => api.startTournament(tournament.id))}>
                  Start ({tournament.duration_ticks} ticks)
                </button>
              </>
            )}
            {(tournament.status === 'finished' || tournament.status === 'running') && (
              <button
                className="btn ghost"
                disabled={busy}
                onClick={() => run(() => api.createTournament(name.trim() || 'welfare-games', duration))}
              >
                New tournament
              </button>
            )}
          </div>
        </>
      )}

      {error && <p className="rationale" style={{ marginTop: 8 }}>{error}</p>}

      {past && past.length > 1 && (
        <details className="t-past">
          <summary className="muted">all tournaments ({past.length})</summary>
          <ul>
            {[...past].reverse().map((t) => (
              <li key={t.id}>
                <span className="muted">{t.created_at.slice(11, 19)}</span> {t.name}{' '}
                <span className={`chip ${t.status}`}>{t.status}</span>
                {t.entries.length > 0 && (
                  <span className="muted">
                    {' '}
                    winner: {shortId(t.entries[0].agent_id)} ({t.entries[0].strategy}, score{' '}
                    {t.entries[0].score.toFixed(3)})
                  </span>
                )}
              </li>
            ))}
          </ul>
        </details>
      )}
    </section>
  )
}
