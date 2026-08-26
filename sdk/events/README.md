# Market events

Significant shocks you can define and throw at the market — a global recession,
a war-like situation, an armageddon — to see how the agents behave under real
stress. Each event is **one scenario document** (one `.md` file in this
directory) with a **machine-readable JSON definition** embedded in a
```` ```json ```` code fence. The JSON is the contract: load it with the SDK,
validate it, and an engine integration applies the effects it describes.

The bundled examples:

| File | Severity | What it is |
|---|---|---|
| [`GLOBAL_RECESSION.md`](GLOBAL_RECESSION.md) | 6/10 · macroeconomic | credit freeze + demand collapse |
| [`WAR_LIKE_SITUATION.md`](WAR_LIKE_SITUATION.md) | 8/10 · geopolitical | energy spike, supply chains crushed |
| [`ARMAGEDDON.md`](ARMAGEDDON.md) | 10/10 · systemic | total loss of confidence |

## Loading events with the SDK

```python
from trading_engine import load_event, load_events

recession = load_event("sdk/events/GLOBAL_RECESSION.md")   # one event
all_events = load_events("sdk/events")                     # every definition
print(recession.headline())
```

`load_event` accepts a `.md` scenario (extracts the fenced JSON) or a bare
`.json` file. Bad definitions raise `trading_engine.EventError` with a
field-level message instead of silently doing nothing.

## The format

```json
{
  "id": "snake_case_slug",
  "name": "Human readable name",
  "severity": 6,
  "kind": "macroeconomic",
  "duration_ticks": 600,
  "news": ["headline", "headline"],
  "rationale": "Why this is mayhem.",
  "shock": {
    "symbols": { "NOVA": -0.30, "ZEPH": 0.45 },
    "ticks": 25,
    "decay": 0.02
  },
  "drift": { "ZEPH": 0.001 },
  "volatility": 2.5,
  "spread_multiplier": 2.0,
  "liquidity": { "levels": 2, "size_multiplier": 0.5 },
  "circuit_breaker": { "drop_pct": 0.20, "halt_ticks": 10 },
  "solidarity": { "gini_target_multiplier": 1.5, "gift_rate_multiplier": 2.0 }
}
```

### Field reference

| Field | Type | Default | Meaning |
|---|---|---|---|
| `id` | string | — (required) | unique slug, `[a-z0-9_]` |
| `name` | string | — (required) | human-readable title |
| `severity` | int 1–10 | — (required) | how bad this is, for dashboards |
| `kind` | string | — (required) | category: `macroeconomic`, `geopolitical`, `systemic`, … |
| `duration_ticks` | int > 0 | — (required) | how long the event is in force (engine ticks ≈ 1 s) |
| `news` | list[string] | `[]` | headlines to broadcast while the event is active |
| `rationale` | string | `""` | why this event wrecks the market |
| `shock.symbols` | map → fraction | `{}` | target move per symbol **as a fraction of fair value** (`-0.30` = −30%). Positive = surge (energy during a war), negative = collapse |
| `shock.ticks` | int > 0 | `1` | ticks over which the shock unfolds (a ramp, not a gap) |
| `shock.decay` | float 0–1 | `0` | exponential decay of the shock after it peaks; `0` holds it for the whole duration |
| `drift` | map → float | `{}` | sustained per-tick directional bias per symbol while active (a grind on top of the shock) |
| `volatility` | float ≥ 1 | `1` | multiplier on the engine's random-walk shock term (fat tails) |
| `spread_multiplier` | float ≥ 1 | `1` | how much wider market-maker spreads get (illiquidity) |
| `liquidity.levels` | int ≥ 0 | — | quote levels the market maker keeps per side during the event |
| `liquidity.size_multiplier` | float 0–1 | — | scale applied to market-maker quote sizes |
| `circuit_breaker.drop_pct` | float 0–1 | — | drop from the event-start reference that trips a trading halt |
| `circuit_breaker.halt_ticks` | int ≥ 0 | — | how long matching is suspended per trip |
| `solidarity.gini_target_multiplier` | float > 0 | — | inequality target tolerated while the event runs (a crash raises it: everyone is poorer together) |
| `solidarity.gift_rate_multiplier` | float > 0 | — | strength of the giving flow during the event |

The effects map onto the engine's real knobs — fair values, the market
maker's spread/size, the random-walk volatility, the solidarity thresholds.
Fields an engine integration doesn't implement yet (e.g. `circuit_breaker`)
are still part of the contract, so every definition says what *should* happen
even before the machinery exists.

## Adding your own event

1. Copy an existing `.md` and keep the same structure: a narrative of the
   real-world pattern, then a fenced ```json block with the definition.
2. Pick a unique `id`, set `severity` and `duration_ticks`, and describe the
   market moves in `shock.symbols` (fractions of fair value, per symbol).
3. Validate it:

```bash
python -m pytest sdk/tests -q          # from the repo root
python sdk/examples/inspect_event.py sdk/events/YOUR_EVENT.md
```

## Validation rules

`trading_engine.Event` enforces the schema strictly:

- required fields must be present with the right types (`id`, `name`,
  `severity` 1–10, `kind`, `duration_ticks` > 0);
- `shock.symbols` and `drift` moves are fractions bounded to `[-1, 1]`;
- `volatility` / `spread_multiplier` must be ≥ 1, liquidity `size_multiplier`
  in `(0, 1]`, `drop_pct` in `(0, 1]`;
- **unknown fields are rejected** — a typo like `"volatlity": 2` raises
  `EventError` at load time instead of being silently ignored.
