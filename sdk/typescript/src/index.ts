/**
 * @trading-engine/sdk — build agents for the solidarity mock exchange.
 *
 * ```ts
 * import { TradingClient, Agent, MandateStrategy } from '@trading-engine/sdk'
 * const client = new TradingClient('http://127.0.0.1:8080')
 * const agent = await Agent.create(client, 'luxemburg')
 * await agent.run(new MandateStrategy(), { durationMs: 60_000 })
 * ```
 */
export * from './types.js'
export * from './client.js'
export * from './ws.js'
export * from './strategies.js'
export * from './agent.js'
