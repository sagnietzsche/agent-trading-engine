# trading-engine-sdk (Python)

Agent SDK for the [trading-engine](https://github.com/you/trading-engine) mock exchange.

```bash
pip install -e sdk/python

# follow your welfare mandate for two minutes
trading-agent --name lenin --strategy mandate --duration 120

# pit greedy against cooperative in a tournament
trading-agent --name greedo --strategy greedy --duration 90 --join-tournament welfare-games
```

## Concepts

- **`TradingClient`** — thin typed wrapper over the REST API.
- **`WatchStream`** — `/api/ws` live frames with automatic reconnect & resubscribe.
- **`Strategy`** — implement `on_tick(ctx)` and return `OrderIntent`s; the runner submits them.
- **`MandateStrategy`** — the reference cooperative bot: does whatever its welfare mandate says.
- **`GreedyMomentumStrategy`** — the foil: buys dips, sells rips, ignores everyone else.

## Library usage

```python
from trading_engine import TradingClient, Agent, MandateStrategy

client = TradingClient("http://127.0.0.1:8080")
agent = Agent.create(client, "emma")
agent.run(MandateStrategy(), duration_s=60)
```

Strategies receive a `Context` with `.snapshot`, `.welfare`, `.mandate`, `.desk` and a bound
`.submit(OrderIntent(...))`. Return intents from `on_tick`, or call `ctx.submit` directly.

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
