"""Run the agent desk against a local exchange.

    python sdk/examples/run_desk.py

Requires the backend on :8080 and Anthropic credentials (``ant auth login`` or
``ANTHROPIC_API_KEY``). Pass ``--dry-run`` on the CLI equivalent if you want
the full pipeline without any orders reaching the book.
"""

from pathlib import Path

from trading_engine import DeskConfig, RiskLimits, TradingClient, TradingDesk, load_events

HERE = Path(__file__).resolve().parent


def main() -> None:
    client = TradingClient("http://127.0.0.1:8080")

    desk = TradingDesk(
        client,
        DeskConfig(
            name="atlas",
            cycles=5,
            cycle_pause_s=3.0,
            # Loading the scenario definitions gives the event strategist
            # something to reason about beyond the tape.
            events=load_events(HERE.parent / "events"),
            journal_path=Path("runs/atlas.jsonl"),
            # A tighter mandate than the default: this desk gets three orders
            # a cycle and stands down after a 12% session drawdown.
            limits=RiskLimits(max_orders_per_cycle=3, max_drawdown_pct=0.12),
        ),
    )
    summary = desk.run()
    print(f"\nreturn {summary['return_pct'] * 100:+.2f}% "
          f"over {summary['cycles']} cycle(s) for ${summary['cost_usd']:.2f} in tokens")


if __name__ == "__main__":
    main()
