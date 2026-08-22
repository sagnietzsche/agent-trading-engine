/**
 * Cooperative reference bot: follows welfare mandates over WebSocket.
 * Run with:  node examples/mandate-bot.ts
 */
import { TradingClient, Agent, MandateStrategy } from '../src/index.js'

const url = process.env.EXCHANGE_URL ?? 'http://127.0.0.1:8080'
const name = process.env.AGENT_NAME ?? 'mandate-ts'
const durationMs = Number(process.env.DURATION_MS ?? 90_000)

const client = new TradingClient(url)
const agent = await Agent.create(client, name)
console.log(`agent ${name} = ${agent.agentId}`)
await agent.run(new MandateStrategy(), { durationMs })

const desk = await client.agent(agent.agentId)
console.log(`final equity: $${desk.equity.toFixed(2)} · role: ${desk.role}`)
