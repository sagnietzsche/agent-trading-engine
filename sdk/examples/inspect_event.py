"""Load an event definition and print its validated market impact.

Usage:
    python sdk/examples/inspect_event.py [path-to-event.md|json]

Defaults to the armageddon scenario bundled with the SDK.
"""

import argparse
import sys
from pathlib import Path

HERE = Path(__file__).resolve().parent
sys.path.insert(0, str(HERE.parent))  # make the SDK importable from anywhere

from trading_engine import EventError, load_event

EVENTS = HERE.parent / "events"


def main() -> int:
    parser = argparse.ArgumentParser(description="inspect a market event definition")
    parser.add_argument("path", nargs="?", default=str(EVENTS / "ARMAGEDDON.md"))
    args = parser.parse_args()

    try:
        ev = load_event(args.path)
    except EventError as exc:
        print(f"invalid event: {exc}", file=sys.stderr)
        return 1

    print(ev.headline())
    print()
    if ev.news:
        print("headlines:")
        for n in ev.news:
            print(f"  ▸ {n}")
        print()
    if ev.rationale:
        print(f"rationale: {ev.rationale}")
        print()

    print("effects:")
    print(f"  fair-value shock : {ev.shock.get('symbols', {})} over {ev.shock.get('ticks', 1)} ticks, decay {ev.shock.get('decay', 0):g}")
    print(f"  drift            : {ev.drift}")
    print(f"  volatility       : x{ev.volatility:g}")
    print(f"  spread           : x{ev.spread_multiplier:g}")
    print(f"  liquidity        : {ev.liquidity}")
    print(f"  circuit breaker  : {ev.circuit_breaker}")
    print(f"  solidarity       : {ev.solidarity}")
    return 0


if __name__ == "__main__":
    sys.exit(main())
