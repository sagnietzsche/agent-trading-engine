"""Replay a desk journal — every decision, in order, with no API key needed.

    python sdk/examples/replay_journal.py runs/atlas.jsonl

The journal is the reason this desk is auditable rather than merely
autonomous. Each line holds one cycle: what the analyst and strategist
concluded, what the PM proposed, what risk and the limit checker cut, and what
actually reached the exchange. When a session goes wrong, this is where you
find out which seat got it wrong.
"""

from __future__ import annotations

import json
import sys
from pathlib import Path


def replay(path: Path) -> int:
    if not path.exists():
        print(f"no journal at {path}", file=sys.stderr)
        return 1

    records = [json.loads(line) for line in path.read_text().splitlines() if line.strip()]
    if not records:
        print("journal is empty")
        return 0

    for r in records:
        print(f"\n── cycle {r['cycle']} · seq {r.get('seq')} · "
              f"equity ${r.get('equity', 0):,.2f} · {r.get('wall_seconds', 0):.1f}s ──")
        print(f"  analyst    : regime={r.get('regime_read') or '—'}")
        print(f"  strategist : {r.get('severity') or '—'} — {r.get('narrative') or '—'}")
        print(f"  pm         : stance={r.get('stance') or '—'}, "
              f"{len(r.get('proposed', []))} proposed")
        for t in r.get("proposed", []):
            print(f"      want {t['side']} {t['qty']} {t['symbol']} :: {t['rationale']}")
        print(f"  risk       : {r.get('risk_overall') or '—'}")
        for line in r.get("rejections", []):
            print(f"      cut  {line}")
        for o in r.get("orders_sent", []):
            avg = f" @ {o['avg_price']:,.2f}" if o.get("avg_price") else ""
            print(f"      SENT {o['side']} {o['qty']} {o['symbol']} "
                  f"→ filled {o.get('filled', 0)}{avg}")
        if r.get("execution_note"):
            print(f"  trader     : {r['execution_note']}")
        for err in r.get("errors", []):
            print(f"  ERROR      : {err}")

    first, last = records[0], records[-1]
    start, end = first.get("equity", 0.0), last.get("equity", 0.0)
    sent = sum(len(r.get("orders_sent", [])) for r in records)
    cut = sum(len(r.get("rejections", [])) for r in records)
    print(f"\n{len(records)} cycle(s): equity ${start:,.0f} → ${end:,.0f} "
          f"({(end / start - 1) * 100:+.2f}%) · {sent} order(s) sent · "
          f"{cut} risk/limit intervention(s)")
    return 0


if __name__ == "__main__":
    target = Path(sys.argv[1] if len(sys.argv) > 1 else "runs/atlas.jsonl")
    raise SystemExit(replay(target))
