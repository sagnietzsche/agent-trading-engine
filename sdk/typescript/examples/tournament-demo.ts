/**
 * Pits a cooperative bot against a greedy one inside a tournament and
 * prints the live scoreboard. Run:  node examples/tournament-demo.ts
 */
import { TradingClient, Agent, MandateStrategy, GreedyMomentumStrategy } from '../src/index.js'
import type { TournamentView } from '../src/index.js'

const url = process.env.EXCHANGE_URL ?? 'http://127.0.0.1:8080'
const ticks = Number(process.env.TICKS ?? 45)
const client = new TradingClient(url)

// 1. create + enter both strategies
const t1 = await client.createTournament('greedy-vs-coop', ticks)
const coop = await Agent.create(client, 'coop-ts')
const greed = await Agent.create(client, 'greed-ts')
await client.enterTournament(t1.id, coop.agentId, 'mandate')
await client.enterTournament(t1.id, greed.agentId, 'greedy')
await client.startTournament(t1.id)
console.log(`tournament ${t1.name} started (${ticks} ticks)\n`)

// 2. watch the live leaderboard from the WS feed while both bots trade
let latest: TournamentView | null = (await client.tournament(t1.id)) ?? null
const streams = [
  coop.run(new MandateStrategy(), { durationMs: (ticks + 3) * 1000 }),
  greed.run(new GreedyMomentumStrategy(), { durationMs: (ticks + 3) * 1000 }),
] as Promise<unknown>[]

const timer = setInterval(() => {
  if (!latest) return
  const rows = latest.entries
    .map((e) => `${e.strategy.padEnd(8)} score=${e.score.toFixed(3)} ret=${(e.return_pct * 100).toFixed(2)}% coop=${(e.coop_share * 100).toFixed(0)}%`)
    .join('\n  ')
  console.log(`--- ticks_left=${latest.ticks_left}\n  ${rows}`)
}, 10_000)

while (latest?.status !== 'finished') {
  await new Promise((r) => setTimeout(r, 1500))
  latest = await client.tournament(t1.id)
}
clearInterval(timer)
await Promise.allSettled(streams)

console.log('\nFINAL —', latest.name)
for (const e of latest.entries) {
  console.log(
    `  ${e.strategy.padEnd(8)} score=${e.score.toFixed(3)} equity=$${e.equity_now.toFixed(0)} ` +
      `ret=${(e.return_pct * 100).toFixed(2)}% coop=${(e.coop_share * 100).toFixed(0)}%`,
  )
}
console.log(`gini: ${latest.gini_start.toFixed(3)} -> ${latest.gini_final?.toFixed(3)}`)
process.exit(0)
