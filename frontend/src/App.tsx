import { useCallback, useEffect, useRef, useState } from 'react'
import './App.css'
import { api } from './api'
import { connectLive, type LiveMode, type Subscription } from './live'
import type { LiveFrame } from './types'
import { WelfareBar } from './components/WelfareBar'
import { StocksTable } from './components/StocksTable'
import { BookLadder } from './components/BookLadder'
import { TapePanel } from './components/TapePanel'
import { AgentsTable } from './components/AgentsTable'
import { TradeTicket } from './components/TradeTicket'
import { MyDesk } from './components/MyDesk'
import { TournamentPanel } from './components/TournamentPanel'
import { DocsPage } from './pages/Docs'

function useRoute(): 'docs' | 'floor' {
  const [path, setPath] = useState(window.location.pathname)
  useEffect(() => {
    const onPop = () => setPath(window.location.pathname)
    window.addEventListener('popstate', onPop)
    return () => window.removeEventListener('popstate', onPop)
  }, [])
  return path.startsWith('/docs') ? 'docs' : 'floor'
}

export default function App() {
  const route = useRoute()
  return route === 'docs' ? <DocsPage /> : <Dashboard />
}

function Dashboard() {
  const [frame, setFrame] = useState<LiveFrame | null>(null)
  const [mode, setMode] = useState<LiveMode>('connecting')
  const [sub, setSub] = useState<Subscription>({
    symbol: 'NOVA',
    agentId: localStorage.getItem('agent_id'),
  })
  const [toast, setToast] = useState<{ msg: string; ok: boolean } | null>(null)
  const [newAgentName, setNewAgentName] = useState('')

  // Keep a ref in sync so the live connection can read the latest
  // subscription without re-connecting.
  const subRef = useRef<Subscription>(sub)
  useEffect(() => {
    subRef.current = sub
  }, [sub])

  const showToast = useCallback((msg: string, ok: boolean) => {
    setToast({ msg, ok })
    window.setTimeout(() => setToast(null), 5000)
  }, [])

  const selectAgent = useCallback((id: string | null) => {
    if (id) localStorage.setItem('agent_id', id)
    else localStorage.removeItem('agent_id')
    setSub((s) => ({ ...s, agentId: id }))
  }, [])

  const selectSymbol = useCallback((symbol: string) => {
    setSub((s) => ({ ...s, symbol }))
  }, [])

  // One live connection drives every panel.
  useEffect(() => {
    return connectLive(
      () => subRef.current,
      (f) => {
        setFrame(f)
        // If our agent vanished (e.g. market reset), re-select a human.
        if (!f.agents.some((a) => a.id === subRef.current.agentId)) {
          selectAgent(f.agents.find((a) => !a.is_bot)?.id ?? null)
        }
      },
      setMode,
    )
  }, [selectAgent])

  const createAgent = async (e: React.FormEvent) => {
    e.preventDefault()
    const name = newAgentName.trim()
    if (!name) return
    try {
      const res = await api.createAgent(name)
      setNewAgentName('')
      selectAgent(res.agent_id)
      showToast(`Agent "${res.name}" joined with $${res.starting_cash.toLocaleString()} — spend it on the collective.`, true)
    } catch (err) {
      showToast(err instanceof Error ? err.message : String(err), false)
    }
  }

  const resetMarket = async () => {
    if (!window.confirm('Wipe the database and restart the market from scratch?')) return
    try {
      await api.reset()
      showToast('Market reset. Fresh books, fresh ledger.', true)
    } catch (err) {
      showToast(err instanceof Error ? err.message : String(err), false)
    }
  }

  const desk = frame?.desk ?? null

  return (
    <div className="shell">
      <header>
        <div>
          <h1>
            trading-engine
            <span className="chip" title={`feed mode: ${mode}`}>
              {mode === 'ws' ? '● live' : mode === 'polling' ? '◐ polling' : '○ connecting'}
            </span>
          </h1>
          <p className="tagline">
            a mock exchange where agents lift each other instead of winning ·{' '}
            <a href="/docs">read the docs →</a>
          </p>
        </div>
        <form className="join" onSubmit={createAgent}>
          <input
            placeholder="new agent name…"
            value={newAgentName}
            onChange={(e) => setNewAgentName(e.target.value)}
          />
          <button className="btn" type="submit">
            Join as agent
          </button>
          <button className="btn danger" type="button" onClick={resetMarket}>
            Reset market
          </button>
        </form>
      </header>

      {frame && <WelfareBar welfare={frame.welfare} history={frame.history ?? []} />}

      <main className="grid">
        <div className="col">
          {frame && (
            <StocksTable stocks={frame.stocks} selected={sub.symbol} onSelect={selectSymbol} />
          )}
          {frame && (
            <AgentsTable
              agents={frame.agents}
              mandates={frame.mandates ?? []}
              selected={sub.agentId}
              onSelect={selectAgent}
            />
          )}
        </div>

        <div className="col">
          <BookLadder book={frame?.book ?? null} />
          <TradeTicket
            stocks={frame?.stocks ?? []}
            agentId={sub.agentId}
            agentDetail={desk}
            onDone={showToast}
          />
        </div>

        <div className="col">
          <MyDesk detail={desk} onChanged={() => undefined} />
          <TournamentPanel tournament={frame?.tournament ?? null} selectedAgent={sub.agentId} />
          {frame && <TapePanel tape={frame.tape} />}
        </div>
      </main>

      {toast && <div className={`toast ${toast.ok ? 'ok' : 'err'}`}>{toast.msg}</div>}

      <footer className="muted">
        Rust · actix-web · SeaORM · Postgres — matched in memory, persisted write-through, streamed
        over WebSocket. <a href="/docs">API &amp; code docs</a>.
      </footer>
    </div>
  )
}
