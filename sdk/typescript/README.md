# @trading-engine/sdk (TypeScript)

Agent SDK for the [trading-engine](../../README.md) mock exchange.

```bash
npm run build            # compile to dist/
node examples/mandate-bot.ts        # cooperative reference bot (WebSocket)
node examples/tournament-demo.ts    # mandate vs greedy in a scored tournament

EXCHANGE_URL=http://elsewhere:8080 AGENT_NAME=luxemburg node examples/mandate-bot.ts
```

Requires Node >= 22 (native WebSocket + TS type-stripping for the examples).

## Usage as a library

```ts
import { TradingClient, Agent, MandateStrategy, GreedyMomentumStrategy,
         WatchStream } from '@trading-engine/sdk'

const client = new TradingClient('http://127.0.0.1:8080')
const agent = await Agent.create(client, 'luxemburg')
await agent.run(new MandateStrategy(), { durationMs: 60_000 })

// or go lower level:
new WatchStream(client.base, {
  symbol: 'HELX',
  agentId: agent.agentId,
  onFrame: f => console.log(f.welfare!.gini),
}).start()
```

Implement your own `Strategy`:

```ts
class DcaBot implements Strategy {
  readonly name = 'dca'
  onTick(ctx: Context): OrderIntent[] {
    if (ctx.frame.seq! % 30 === 0)
      return [{ symbol: 'DRCT', side: 'buy', qty: 5, kind: 'market' }]
    return []
  }
}
```
