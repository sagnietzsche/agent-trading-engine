"""trading-desk: run a team of LLM agents against the exchange.

The desk registers an account, then runs the analyst → strategist → PM → risk
→ execution pipeline once per cycle. Every decision is written to a JSONL
journal, and the run ends with a per-agent token and cost breakdown.

Examples:
    trading-desk --name atlas --cycles 5
    trading-desk --name atlas --cycles 20 --events sdk/events --journal runs/atlas.jsonl
    trading-desk --name atlas --dry-run          # decide everything, send nothing
    trading-desk --name atlas --max-drawdown 0.10 --max-orders 3
"""

from __future__ import annotations

import argparse
import sys
from pathlib import Path

from .client import TradingClient
from .desk import DeskConfig, TradingDesk
from .events import load_events
from .llm import credentials_available
from .risk import RiskLimits


def build_parser() -> argparse.ArgumentParser:
    p = argparse.ArgumentParser(prog="trading-desk", description=__doc__,
                                formatter_class=argparse.RawDescriptionHelpFormatter)
    p.add_argument("--url", default="http://127.0.0.1:8080", help="exchange base URL")
    p.add_argument("--name", default="atlas", help="desk account name (created if missing)")
    p.add_argument("--symbol", default="NOVA", help="symbol whose book is shown in the briefing")
    p.add_argument("--cycles", type=int, default=5, help="pipeline cycles to run")
    p.add_argument("--pause", type=float, default=2.0, help="seconds between cycles")
    p.add_argument("--events", metavar="DIR", help="directory of event definitions to load")
    p.add_argument("--journal", metavar="PATH", help="JSONL decision log to append to")
    p.add_argument("--model", default="claude-opus-5", help="Claude model for every agent")
    p.add_argument("--dry-run", action="store_true",
                   help="run the full pipeline but send no orders")
    g = p.add_argument_group("risk mandate")
    g.add_argument("--max-gross", type=float, default=0.85, help="max gross exposure / equity")
    g.add_argument("--max-position", type=float, default=0.30, help="max single name / equity")
    g.add_argument("--max-order", type=float, default=0.15, help="max single order / equity")
    g.add_argument("--max-orders", type=int, default=6, help="max orders per cycle")
    g.add_argument("--cash-buffer", type=float, default=0.10, help="cash floor / equity")
    g.add_argument("--max-drawdown", type=float, default=0.20, help="session halt threshold")
    return p


def main(argv: list[str] | None = None) -> int:
    args = build_parser().parse_args(argv)

    client = TradingClient(args.url)
    try:
        health = client.health()
    except Exception as err:  # noqa: BLE001
        print(f"exchange unreachable at {args.url}: {err}", file=sys.stderr)
        return 1

    if not credentials_available():
        print(
            "No Anthropic credentials found. Every agent on this desk is a Claude "
            "model, so the desk cannot run without them.\n"
            "  ant auth login          (stores a profile the SDK reads automatically)\n"
            "  export ANTHROPIC_API_KEY=...",
            file=sys.stderr,
        )
        return 1

    events = load_events(Path(args.events)) if args.events else ()

    config = DeskConfig(
        name=args.name,
        symbol=args.symbol,
        cycles=args.cycles,
        cycle_pause_s=args.pause,
        journal_path=Path(args.journal) if args.journal else None,
        events=events,
        dry_run=args.dry_run,
        model=args.model,
        limits=RiskLimits(
            max_gross_exposure=args.max_gross,
            max_position_pct=args.max_position,
            max_order_notional_pct=args.max_order,
            max_orders_per_cycle=args.max_orders,
            min_cash_buffer_pct=args.cash_buffer,
            max_drawdown_pct=args.max_drawdown,
        ),
    )

    print(f"venue: {args.url} · regime={health.get('regime', '?')} "
          f"metric={health.get('metric', '?')} · model={args.model}")
    if events:
        print(f"scenarios loaded: {', '.join(e.id for e in events)}")
    if args.dry_run:
        print("DRY RUN — the pipeline runs in full, but no orders reach the exchange")

    desk = TradingDesk(client, config)
    try:
        desk.run()
    except KeyboardInterrupt:
        print("\ninterrupted — journal is on disk", file=sys.stderr)
        return 130
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
