# trading-engine-sdk (Python)

Agent SDK for the trading-engine mock exchange.

```bash
pip install -e sdk

# follow your welfare mandate for two minutes
trading-agent --name lenin --strategy mandate --duration 120

# pit greedy against cooperative in a tournament
trading-agent --name greedo --strategy greedy --duration 90 --join-tournament welfare-games
```

## Concepts

- **`TradingClient`** — typed REST wrapper over every endpoint.
- **`WatchStream`** — `/api/ws` live frames with automatic reconnect & resubscribe.
- **`Strategy`** — implement `on_tick(ctx)` and return `OrderIntent`s; the runner submits them.
- **`MandateStrategy`** — the reference cooperative bot: does whatever its welfare mandate says, and reports each new instruction in the floor chat.
- **`GreedyMomentumStrategy`** — the foil: buys dips, sells rips, ignores everyone else.
- **`Event` / `load_event` / `load_events`** — validated market-event definitions from `sdk/events/`.

## Library usage

```python
from trading_engine import TradingClient, Agent, MandateStrategy

client = TradingClient("http://127.0.0.1:8080")
agent = Agent.create(client, "emma")
agent.run(MandateStrategy(), duration_s=60)
```

Strategies receive a `Context` with `.snapshot`, `.welfare`, `.mandate`, `.desk` and a bound
`.submit(OrderIntent(...))`. Return intents from `on_tick`, or call `ctx.submit` directly.

### Floor chat

Agents write to the floor chatroom when they act on instructions, and you can monitor them:

```python
client.say(agent_id, "✊ Following my mandate: sell 10 NOVA at 184.10")
client.chat(limit=30)          # recent messages, newest first
client.announce("Everyone give 5%")   # broadcast; the bots reply in chat
```

The bundled `MandateStrategy` reports each new mandate it follows, and
`GreedyMomentumStrategy` brags when it trades — watch both from the TUI's
chat panel (`c` to open, `a` to announce).

## Market events

Significant shocks — a global recession, a war-like situation, an armageddon — are defined as
event files in `sdk/events/` (scenario docs with a machine-readable JSON definition embedded in
each). Load and validate them with the SDK:

```python
from trading_engine import load_event, load_events

recession = load_event("sdk/events/GLOBAL_RECESSION.md")
all_events = load_events("sdk/events")
print(recession.headline())
```

`load_event` accepts `.md` scenario docs or bare `.json` files and enforces the event schema
strictly — a typo in a field name raises `EventError` at load time. See `sdk/events/README.md`
for the full format reference, and `python sdk/examples/inspect_event.py` for a quick demo.
