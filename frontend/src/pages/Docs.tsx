import { useEffect, useRef, useState } from 'react'
import {
  CODEBASE_GUIDE,
  DATA_FLOW,
  ENDPOINTS,
  WS_CLIENT_MESSAGES,
  WS_FRAME_EXAMPLE,
  type EndpointDoc,
} from '../docs/content'

function Code({ children }: { children: string }) {
  return <pre className="docs-code">{children}</pre>
}

function TryIt({ path }: { path: string }) {
  const [out, setOut] = useState<string | null>(null)
  const [busy, setBusy] = useState(false)
  const run = async () => {
    setBusy(true)
    try {
      const res = await fetch(path.startsWith('/api') ? path : `/api${path}`)
      const body = await res.json()
      setOut(`HTTP ${res.status}\n${JSON.stringify(body, null, 2).slice(0, 4000)}`)
    } catch (e) {
      setOut(String(e))
    } finally {
      setBusy(false)
    }
  }
  return (
    <div className="tryit">
      <button className="btn ghost" onClick={run} disabled={busy}>
        {busy ? 'running…' : '▶ try it'}
      </button>
      {out && <Code>{out}</Code>}
    </div>
  )
}

function EndpointCard({ ep }: { ep: EndpointDoc }) {
  const color = ep.method === 'GET' ? 'up' : ep.method === 'DELETE' ? 'down' : ''
  return (
    <article className="ep-card">
      <h3>
        <span className={`method ${color}`}>{ep.method}</span>
        <code>{ep.path}</code>
      </h3>
      <p>{ep.summary}</p>
      {ep.details?.map((d, i) => (
        <p key={i} className="muted">
          {d}
        </p>
      ))}
      {ep.params && ep.params.length > 0 && (
        <table className="tbl compact params">
          <thead>
            <tr>
              <th>param</th>
              <th>in</th>
              <th>type</th>
              <th>notes</th>
            </tr>
          </thead>
          <tbody>
            {ep.params.map((p) => (
              <tr key={p.name}>
                <td>
                  <code>{p.name}</code>
                  {p.required && <b className="req">*</b>}
                </td>
                <td className="muted">{p.location}</td>
                <td className="muted">{p.type}</td>
                <td className="muted">{p.description || '—'}</td>
              </tr>
            ))}
          </tbody>
        </table>
      )}
      {ep.request && (
        <>
          <h4>request</h4>
          <Code>{ep.request}</Code>
        </>
      )}
      {ep.response && (
        <>
          <h4>response</h4>
          <Code>{ep.response}</Code>
        </>
      )}
      {ep.tryIt && <TryIt path={ep.tryIt} />}
    </article>
  )
}

/** Connects to /api/ws and shows the raw frames — the protocol, live. */
function WsConsole() {
  const [status, setStatus] = useState<'idle' | 'connecting' | 'open' | 'error'>('idle')
  const [frames, setFrames] = useState<string[]>([])
  const [count, setCount] = useState(0)
  const wsRef = useRef<WebSocket | null>(null)

  useEffect(() => () => wsRef.current?.close(), [])

  const connect = () => {
    wsRef.current?.close()
    setStatus('connecting')
    const proto = location.protocol === 'https:' ? 'wss' : 'ws'
    const ws = new WebSocket(`${proto}://${location.host}/api/ws`)
    wsRef.current = ws
    ws.onopen = () => {
      setStatus('open')
      ws.send(JSON.stringify({ type: 'subscribe', symbol: 'NOVA', agent_id: null }))
    }
    ws.onmessage = (ev) => {
      let pretty = ev.data as string
      try {
        pretty = JSON.stringify(JSON.parse(ev.data as string), null, 2)
      } catch {
        /* leave as-is */
      }
      setFrames((prev) => [pretty.slice(0, 2600), ...prev].slice(0, 4))
      setCount((c) => c + 1)
    }
    ws.onerror = () => setStatus('error')
    ws.onclose = () => setStatus((s) => (s === 'error' ? s : 'idle'))
  }

  return (
    <div className="ws-console">
      <div className="ws-bar">
        <button className="btn ghost" onClick={connect} disabled={status === 'open'}>
          connect
        </button>
        <button className="btn ghost" onClick={() => wsRef.current?.close()} disabled={status === 'idle'}>
          close
        </button>
        <span className={`chip ${status === 'open' ? 'beneficiary' : status === 'error' ? 'contributor' : ''}`}>
          {status}
          {status === 'open' && ` · ${count} frames`}
        </span>
      </div>
      {frames.map((f, i) => (
        <details key={`${count}-${i}`} open={i === 0}>
          <summary className="muted">frame −{i}</summary>
          <Code>{f}</Code>
        </details>
      ))}
    </div>
  )
}

const NAV = [
  ['start', 'Getting started'],
  ['concepts', 'Core concepts'],
  ['rest', 'REST API'],
  ['ws', 'WebSocket feed'],
  ['tournament', 'Tournaments'],
  ['sdks', 'Agent SDKs'],
  ['codebase', 'Codebase guide'],
] as const

export function DocsPage() {
  const [active, setActive] = useState<string>('start')

  const go = (id: string) => {
    setActive(id)
    document.getElementById(id)?.scrollIntoView({ behavior: 'smooth', block: 'start' })
  }

  return (
    <div className="shell docs">
      <header>
        <div>
          <h1>/docs</h1>
          <p className="tagline">every endpoint, every concept, and how the code fits together</p>
        </div>
        <a className="btn ghost" href="/">
          ← back to trading floor
        </a>
      </header>

      <div className="docs-body">
        <nav className="docs-nav">
          {NAV.map(([id, label]) => (
            <a
              key={id}
              href={`#${id}`}
              className={active === id ? 'on' : ''}
              onClick={(e) => {
                e.preventDefault()
                go(id)
              }}
            >
              {label}
            </a>
          ))}
        </nav>

        <main className="docs-main">
          <section id="start">
            <h2>Getting started</h2>
            <p>
              Base URL: <code>{location.origin}/api</code> (the dev server proxies to the Go
              backend on <code>:8080</code>). No authentication — this is an open sandbox; any
              caller may act as any agent.
            </p>
            <Code>{`# start everything
docker compose up -d                 # postgres
cd backend && go run .               # api + matching engine :8080
cd frontend && npm run dev           # ui :5173

# join & trade in three calls
curl -s $B/api/agents -X POST -H 'content-type: application/json' -d '{"name":"me"}'
curl -s $B/api/welfare | jq '.agents[] | {name, role}'     # read your mandate
curl -s $B/api/orders -X POST -H 'content-type: application/json' \\
  -d '{"agent_id":"…","symbol":"NOVA","side":"buy","kind":"limit","qty":10,"price":184.5}'`}</Code>
          </section>

          <section id="concepts">
            <h2>Core concepts</h2>
            <div className="concept-grid">
              <div className="concept">
                <h3>Matching engine</h3>
                <p>
                  One book per symbol with <b>price-time priority</b>. Limit orders rest; market
                  orders sweep what crosses and cancel the rest (IOC). Partial fills everywhere.
                  Buyers reserve cash, sellers reserve shares at placement — you can never promise
                  money you don't have. Fills settle at the <i>maker's</i> price.
                </p>
              </div>
              <div className="concept">
                <h3>Welfare objective</h3>
                <p>
                  The engine measures inequality with the <b>Gini coefficient</b> over agent
                  equities. Agents more than 10% above the mean are <i>contributors</i>, more than 10% below are{' '}
                  <i>beneficiaries</i>. Every trade stores both parties' equity plus the post-trade
                  Gini — the tape is an inequality ledger.
                </p>
              </div>
              <div className="concept">
                <h3>Mandates</h3>
                <p>
                  <code>GET /api/welfare</code> gives each agent a suggested trade: contributors
                  sell inventory <b>at the bid</b> (the price concession is the gift), beneficiaries
                  buy <b>at the ask</b> using part of their shortfall. The UI's “Follow my mandate”
                  button submits it for you.
                </p>
              </div>
              <div className="concept">
                <h3>Need-priority matching</h3>
                <p>
                  Solidarity orders skip the queue: if any resting counterparty belongs to a
                  beneficiary, they fill first even when a better-priced neutral quote exists. This
                  is how gifts reach the worst-off instead of being intercepted by middlemen. The{' '}
                  <code>solidarity_bot</code> uses it every tick while Gini exceeds target.
                </p>
              </div>
              <div className="concept">
                <h3>Persistence</h3>
                <p>
                  Matching happens in memory; every mutation is flushed to Postgres in one
                  transaction of idempotent upserts. On restart the books, balances, open orders
                  and running tournaments are rebuilt from rows — kill the process mid-session and
                  nothing is lost.
                </p>
              </div>
              <div className="concept">
                <h3>Simulation</h3>
                <p>
                  A background task ticks each second: random-walks fair values, requotes a neutral
                  market maker (tight spreads as a public good), fires solidarity flow, advances
                  tournaments, and snapshots welfare for the trend chart.
                </p>
              </div>
            </div>
          </section>

          <section id="rest">
            <h2>REST API</h2>
            <p className="muted">
              Errors are always <code>{`{"error": "…"}`}</code> with 400/404/500. GET cards have a ▶
              button that executes against the running backend.
            </p>
            {ENDPOINTS.map((ep) => (
              <EndpointCard key={`${ep.method}-${ep.path}`} ep={ep} />
            ))}
          </section>

          <section id="ws">
            <h2>WebSocket feed</h2>
            <p>
              <code>GET /api/ws?symbol=NOVA&amp;agent_id=…</code> upgrades to WebSocket. The server
              pushes a full <code>snapshot</code> frame every second; optional fields appear on
              extended frames or when subscribed with an <code>agent_id</code>.
            </p>
            <h4>server → client frame</h4>
            <Code>{WS_FRAME_EXAMPLE}</Code>
            <h4>client → server messages</h4>
            <Code>{WS_CLIENT_MESSAGES}</Code>
            <h4>live console</h4>
            <WsConsole />
          </section>

          <section id="tournament">
            <h2>Tournaments</h2>
            <p>
              A tournament is a timed competition between enrolled strategies scored under the{' '}
              <b>welfare objective</b>, not raw profit:
            </p>
            <Code>{`score = RETURN_WEIGHT × equity_return + COOP_WEIGHT × coop_share

equity_return = equity_end / equity_start − 1
coop_share    = prosocial_volume / total_volume

A fill counts as *prosocial* for whichever side is WEALTHIER:
richer seller → poorer buyer  ⇒ seller earns coop credit (giving discount)
richer buyer  → poorer seller ⇒ buyer earns coop credit (paying up)`}</Code>
            <p>
              Defaults: <code>RETURN_WEIGHT = COOP_WEIGHT = 1.0</code> — cooperation can fully
              offset a modest loss, so a pure profit-maximizer loses to a strategy that trades with
              poorer members. Lifecycle: <code>create → enter (while open) → start → runs N sim
              ticks → finalize (scores persisted)</code>. Baselines are captured at start, not
              signup. Watch the leaderboard update live on the dashboard via the WS frame's{' '}
              <code>tournament</code> field.
            </p>
          </section>

          <section id="sdks">
            <h2>Agent SDKs</h2>
            <p>
              Two first-class SDKs implement the same concepts: REST client, live-frame stream,
              pluggable <code>Strategy</code>, a reference <code>MandateStrategy</code> that plays
              along with the collective, and tournament helpers.
            </p>
            <h4>Python — sdk/python</h4>
            <Code>{`pip install -e sdk/python

# CLI: join, follow mandates, optionally enter a tournament
python -m trading_engine.cli --name lenin --strategy mandate --duration 120 \\
       --join-tournament welfare-games

# or embed the library
from trading_engine import TradingClient, MandateStrategy, Agent
client = TradingClient("http://127.0.0.1:8080")
agent  = Agent.create(client, "emma")
agent.run(MandateStrategy(), duration_s=60)`}</Code>
            <h4>TypeScript — sdk/typescript</h4>
            <Code>{`cd sdk/typescript && npm i && npm run build   # Node >= 22 (native WebSocket)

# cooperative reference bot over WebSocket
node examples/mandate-bot.ts

# mandate vs greedy inside a scored tournament, live scoreboard
node examples/tournament-demo.ts

import { TradingClient, Agent, MandateStrategy } from "@trading-engine/sdk";
const client = new TradingClient("http://127.0.0.1:8080");
const agent  = await Agent.create(client, "luxemburg");
await agent.run(new MandateStrategy(), { durationMs: 60_000 });`}</Code>
            <p className="muted">
              Both CLIs accept <code>--strategy mandate|greedy</code> so you can pit a cooperative
              bot against a greedy one inside a tournament and watch the scoreboard decide.
            </p>
          </section>

          <section id="codebase">
            <h2>Codebase guide</h2>
            <Code>{DATA_FLOW}</Code>
            <table className="tbl">
              <thead>
                <tr>
                  <th>file</th>
                  <th>what lives there</th>
                </tr>
              </thead>
              <tbody>
                {CODEBASE_GUIDE.map((row) => (
                  <tr key={row.file}>
                    <td>
                      <code>{row.file}</code>
                    </td>
                    <td className="muted">{row.role}</td>
                  </tr>
                ))}
              </tbody>
            </table>
          </section>
        </main>
      </div>
    </div>
  )
}
