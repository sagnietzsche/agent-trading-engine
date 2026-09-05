# trading-engine-sdk (Python)

An autonomous trading desk for the [trading-engine](../README.md) mock exchange:
five Claude agents with one job each, wired together by typed contracts and
bounded by risk limits enforced in code.

```bash
pip install -e .          # from sdk/
trading-desk --name atlas --cycles 20 --events events --journal runs/atlas.jsonl
```

Requires Python 3.10+, a running exchange, and Anthropic credentials
(`ant auth login`, or `ANTHROPIC_API_KEY`).

---

## The pipeline

Each cycle runs five agents in order. Only the last one can touch the exchange.

| Seat | Module | Produces | Effort | Tools |
|---|---|---|---|---|
| Market analyst | `agents/analyst.py` | `MarketRead` | high | — |
| Event strategist | `agents/strategist.py` | `MacroRead` | high | — |
| Portfolio manager | `agents/pm.py` | `PortfolioPlan` | high | — |
| Risk officer | `agents/risk.py` | `RiskAssessment` | medium | — |
| Execution trader | `agents/trader.py` | fills | low | place / cancel / read book |

The analyst and strategist have independent inputs, so they run concurrently.
Everything after them is sequential because each seat needs the previous one's
output.

After the risk officer rules, `risk.enforce()` clamps every ticket again in
code — single-order notional, per-name concentration, gross exposure, cash
buffer, free-share check, orders per cycle, and a session drawdown kill switch.
The LLM can shrink a ticket; only code can be trusted to.

## Quick start

```python
from pathlib import Path
from trading_engine import DeskConfig, RiskLimits, TradingClient, TradingDesk, load_events

desk = TradingDesk(
    TradingClient("http://127.0.0.1:8080"),
    DeskConfig(
        name="atlas",
        cycles=20,
        cycle_pause_s=3.0,
        events=load_events("events"),          # input for the event strategist
        journal_path=Path("runs/atlas.jsonl"), # replayable audit trail
        limits=RiskLimits(max_orders_per_cycle=3, max_drawdown_pct=0.12),
    ),
)
summary = desk.run()
print(summary["return_pct"], summary["cost_usd"])
```

## CLI

```
trading-desk [--url URL] [--name NAME] [--symbol SYM] [--cycles N] [--pause S]
             [--events DIR] [--journal PATH] [--model MODEL] [--dry-run]
             [--max-gross F] [--max-position F] [--max-order F]
             [--max-orders N] [--cash-buffer F] [--max-drawdown F]
```

`--dry-run` runs the entire pipeline — every model call, every risk ruling —
and sends nothing. It is the cheapest way to read what the desk *would* do.

## Modules

| Module | What it holds |
|---|---|
| `desk.py` | `TradingDesk` / `DeskConfig` — the cycle, error containment, session summary |
| `agents/` | the five seats; each is a charter plus an output schema |
| `schemas.py` | the pydantic contract at every seam |
| `risk.py` | `RiskLimits`, `RiskState`, `ApprovedTicket`, `enforce()` |
| `llm.py` | `DeskLLM`, `ModelPolicy`, `Ledger` — model policy, caching, cost accounting |
| `brief.py` | `venue_manual()` (the cached prefix) and the per-cycle briefing |
| `journal.py` | `Journal` / `CycleRecord` — JSONL audit trail and rolling memory |
| `client.py` | `TradingClient` — typed REST wrapper over every endpoint |
| `ws.py` | `WatchStream` — `/api/ws` frames with reconnect and resubscribe |
| `events.py` | `load_event` / `load_events` — strict scenario-definition loader |

## Swapping a seat

An agent is a `charter` property and a schema, so overriding one is a subclass:

```python
from trading_engine.agents import RiskOfficer

class DrawdownHawk(RiskOfficer):
    @property
    def charter(self) -> str:
        return super().charter + "\n* Reject any market order.\n"

desk.risk = DrawdownHawk(desk.llm, desk.manual)
```

Extending `super().charter` rather than replacing it keeps the output-contract
rules (one verdict per trade, never enlarge) intact. See
[`examples/custom_seat.py`](examples/custom_seat.py).

## Examples

| Script | What it shows |
|---|---|
| `examples/run_desk.py` | a full session, programmatically configured |
| `examples/replay_journal.py` | walk a finished session's decisions — **no API key needed** |
| `examples/custom_seat.py` | replacing one agent without touching the rest |
| `examples/inspect_event.py` | an event definition's full effects table |

## Market events

Scenario definitions live in [`events/`](events/README.md): a document with a
machine-readable JSON block describing fair-value shocks per symbol,
volatility and spread multipliers, liquidity squeezes, and circuit breakers.

```python
from trading_engine import load_event, load_events

armageddon = load_event("events/ARMAGEDDON.md")
print(armageddon.headline())
all_events = load_events("events")
```

The loader is strict — unknown fields and out-of-range values raise
`EventError` at load time, so a typo cannot silently do nothing.

## Tests

```bash
pip install -e '.[dev]'
python -m pytest tests
```

The suite runs **without credentials or a network**. Model calls are replaced
by a stub returning canned schema instances, which is what the typed seams are
for: if the contract holds for a stub, it holds for the model. Covered: the
full pipeline, the risk clamp in isolation, the execution tool gate (unapproved
symbol, flipped side, exceeded ticket, slicing), the drawdown kill switch,
briefing rendering in degenerate markets, cached-prefix stability, and the
event loader.
