"""trading-agent: run a strategy bot from the command line.

Examples:
    trading-agent --name lenin --strategy mandate --duration 120
    trading-agent --name greedo --strategy greedy --duration 90 \
        --join-tournament welfare-games --start
"""

from __future__ import annotations

import argparse
import sys
import time

from .agent import STRATEGIES, Agent
from .client import TradingClient


def find_or_create_tournament(client: TradingClient, name: str, duration: int) -> str:
    for t in client.tournaments():
        if t["name"] == name and t["status"] in ("open", "running"):
            return t["id"]
    return client.create_tournament(name, duration)["id"]


def main(argv: list[str] | None = None) -> int:
    parser = argparse.ArgumentParser(prog="trading-agent", description=__doc__)
    parser.add_argument("--url", default="http://127.0.0.1:8080")
    parser.add_argument("--name", required=True, help="agent name (created if missing)")
    parser.add_argument("--strategy", choices=sorted(STRATEGIES), default="mandate")
    parser.add_argument("--symbol", default="NOVA")
    parser.add_argument("--duration", type=float, default=60.0, help="seconds to run")
    parser.add_argument("--no-ws", action="store_true", help="poll REST instead of WebSocket")
    parser.add_argument("--join-tournament", metavar="NAME", help="enter/create a tournament")
    parser.add_argument("--start", action="store_true", help="start the tournament after entering")
    args = parser.parse_args(argv)

    client = TradingClient(args.url)
    try:
        client.health()
    except Exception as err:  # noqa: BLE001
        print(f"exchange unreachable at {args.url}: {err}", file=sys.stderr)
        return 1

    agent_id = next(
        (a["id"] for a in client.snapshot()["agents"] if a["name"] == args.name), None
    )
    agent = Agent(client, agent_id, args.name) if agent_id else Agent.create(client, args.name)
    print(f"agent {args.name} = {agent.agent_id}")

    if args.join_tournament:
        tid = find_or_create_tournament(client, args.join_tournament, max(5, int(args.duration) + 15))
        status = client.tournament(tid)["status"]
        client.enter_tournament(tid, agent.agent_id, args.strategy)
        print(f"entered tournament {args.join_tournament} ({tid})")
        if args.start and status == "open":
            client.start_tournament(tid)
            print("tournament started")

    strategy = STRATEGIES[args.strategy]()
    started = time.monotonic()
    stats = agent.run(strategy, duration_s=args.duration, symbol=args.symbol, use_ws=not args.no_ws)
    elapsed = time.monotonic() - started

    desk = client.agent(agent.agent_id)
    print(
        f"\ndone in {elapsed:.0f}s — ticks={stats['ticks']} orders={stats['orders']} "
        f"equity=${desk['equity']:,.2f} cash=${desk['cash']:,.2f}"
    )
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
