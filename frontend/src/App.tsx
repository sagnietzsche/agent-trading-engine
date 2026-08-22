import { useCallback, useEffect, useRef, useState } from 'react'
import { api } from './api'
import type { AgentDetail, Snapshot, WelfareResp } from './types'
import { WelfareBar } from './components/WelfareBar'
import { StocksTable } from './components/StocksTable'
import { BookLadder } from './components/BookLadder'
import { TapePanel } from './components/TapePanel'
import { AgentsTable } from './components/AgentsTable'
import { TradeTicket } from './components/TradeTicket'
import { MyDesk } from './components/MyDesk'

const POLL_MS = 1200
const WELFARE_EVERY = 3

export default function App() {
  const [snapshot, setSnapshot] = useState<Snapshot | null>(null)
  const [welfare, setWelfare] = useState<WelfareResp | null>(null)
  const [agentDetail, setAgentDetail] = useState<AgentDetail | null>(null)
  const [selectedSymbol, setSelectedSymbol] = useState('NOVA')
  const [selectedAgent, setSelectedAgent] = useState<string | null>(() =>
    localStorage.getItem('agent_id'),
  )
  const [toast, setToast] = useState<{ msg: string; ok: boolean } | null>(null)
  const [newAgentName, setNewAgentName] = useState('')
  const tick = useRef(0)

  const showToast = useCallback((msg: string, ok: boolean) => {
    setToast({ msg, ok })
    window.setTimeout(() => setToast(null), 5000)
  }, [])

  const selectAgent = useCallback((id: string | null) => {
    setSelectedAgent(id)
    if (id) localStorage.setItem('agent_id', id)
    else localStorage.removeItem('agent_id')
  }, [])

  // Polling loop: snapshot every tick, welfare + agent desk periodically.
  useEffect(() => {
    let alive = true
    const poll = async () => {
      try {
        const snap = await api.snapshot(selectedSymbol)
        if (!alive) return
        setSnapshot(snap)
        if (!snap.agents.some((a) => a.id === selectedAgent)) {
          const human = snap.agents.find((a) => !a.is_bot)
          selectAgent(human?.id ?? null)
          return
        }
        const n = ++tick.current
        if (n % WELFARE_EVERY === 0) setWelfare(await api.welfare())
        if (selectedAgent) setAgentDetail(await api.agent(selectedAgent))
      } catch {
        /* backend briefly unavailable; keep last frame */
      }
    }
    poll()
    const t = window.setInterval(poll, POLL_MS)
    return () => {
      alive = false
      window.clearInterval(t)
    }
  }, [selectedSymbol, selectedAgent, selectAgent])

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

  return (
    <div className="shell">
      <header>
        <div>
          <h1>trading-engine</h1>
          <p className="tagline">
            a mock exchange where agents are rewarded for lifting each other, not for winning
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

      {welfare && <WelfareBar welfare={welfare.welfare} history={welfare.history} />}

      <main className="grid">
        <div className="col">
          {snapshot && (
            <StocksTable stocks={snapshot.stocks} selected={selectedSymbol} onSelect={setSelectedSymbol} />
          )}
          {snapshot && (
            <AgentsTable
              agents={snapshot.agents}
              mandates={welfare?.agents ?? []}
              selected={selectedAgent}
              onSelect={selectAgent}
            />
          )}
        </div>

        <div className="col">
          <BookLadder book={snapshot?.book ?? null} />
          <TradeTicket
            stocks={snapshot?.stocks ?? []}
            agentId={selectedAgent}
            agentDetail={agentDetail}
            onDone={showToast}
          />
        </div>

        <div className="col">
          <MyDesk detail={agentDetail} onChanged={() => undefined} />
          {snapshot && <TapePanel tape={snapshot.tape} />}
        </div>
      </main>

      {toast && <div className={`toast ${toast.ok ? 'ok' : 'err'}`}>{toast.msg}</div>}

      <footer className="muted">
        Rust · actix-web · SeaORM · Postgres — matching runs in memory and persists every order,
        trade and balance to the database.
      </footer>
    </div>
  )
}
