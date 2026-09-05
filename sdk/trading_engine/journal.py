"""The desk's audit trail and its working memory.

Two jobs, one file. Every cycle is appended to a JSONL run log — the full
input brief, each agent's structured output, what risk cut, what actually
filled — so any decision can be reconstructed after the fact. That same record
is then summarized back into the next cycle's prompt, which is how the desk
gets any continuity at all: an LLM agent has no memory between calls, so
whatever it should remember has to be handed to it again.

Deliberately kept to the last few cycles. A journal that grows without bound
would push the prompt past the point where the model attends to it, and would
invalidate the prompt cache on every single cycle.
"""

from __future__ import annotations

import json
import time
from collections import deque
from dataclasses import asdict, dataclass, field
from pathlib import Path
from typing import Any, Deque

__all__ = ["CycleRecord", "Journal"]


@dataclass
class CycleRecord:
    """Everything one cycle of the pipeline decided and did."""

    cycle: int
    seq: Any = None
    equity: float = 0.0
    stance: str = ""
    regime_read: str = ""
    narrative: str = ""
    severity: str = ""
    risk_overall: str = ""
    proposed: list[dict[str, Any]] = field(default_factory=list)
    approved: list[dict[str, Any]] = field(default_factory=list)
    rejections: list[str] = field(default_factory=list)
    orders_sent: list[dict[str, Any]] = field(default_factory=list)
    execution_note: str = ""
    errors: list[str] = field(default_factory=list)
    wall_seconds: float = 0.0
    ts: str = field(default_factory=lambda: time.strftime("%Y-%m-%dT%H:%M:%SZ", time.gmtime()))


class Journal:
    """Append-only run log plus a short rolling memory for prompts."""

    def __init__(self, path: str | Path | None = None, memory_cycles: int = 4) -> None:
        self.path = Path(path) if path else None
        self.memory_cycles = memory_cycles
        self._recent: Deque[CycleRecord] = deque(maxlen=memory_cycles)
        self.all: list[CycleRecord] = []
        if self.path is not None:
            self.path.parent.mkdir(parents=True, exist_ok=True)

    def append(self, record: CycleRecord) -> None:
        self._recent.append(record)
        self.all.append(record)
        if self.path is not None:
            with self.path.open("a", encoding="utf-8") as fh:
                fh.write(json.dumps(asdict(record), default=str) + "\n")

    def recall(self, equity_now: float) -> str:
        """Render recent history as prompt context for the PM.

        Includes the realized equity change per cycle, which is the only
        feedback signal the desk has about whether its last decisions were any
        good — without it the PM is choosing in a vacuum every cycle.
        """
        if not self._recent:
            return "## Recent history\n(this is the first cycle of the session)"
        lines = ["## Recent history (oldest first)"]
        previous: float | None = None
        for rec in self._recent:
            delta = ""
            if previous is not None and previous > 0:
                delta = f" equity {(rec.equity / previous - 1.0) * 100:+.2f}% vs prior cycle"
            previous = rec.equity
            sent = ", ".join(
                f"{o['side']} {o['qty']} {o['symbol']}" for o in rec.orders_sent
            ) or "no orders"
            lines.append(
                f"- cycle {rec.cycle}: stance={rec.stance or '?'} "
                f"risk={rec.risk_overall or '?'} narrative={rec.narrative or 'none'!r} "
                f"→ {sent}.{delta}"
            )
            if rec.rejections:
                lines.append(f"  risk/limits intervened: {'; '.join(rec.rejections[:3])}")
            if rec.errors:
                lines.append(f"  errors: {'; '.join(rec.errors[:2])}")
        if previous is not None and previous > 0:
            lines.append(
                f"- now: equity ${equity_now:,.0f} "
                f"({(equity_now / previous - 1.0) * 100:+.2f}% since last cycle)"
            )
        return "\n".join(lines)

    def summary(self) -> dict[str, Any]:
        sent = sum(len(r.orders_sent) for r in self.all)
        return {
            "cycles": len(self.all),
            "orders_sent": sent,
            "risk_interventions": sum(len(r.rejections) for r in self.all),
            "errors": sum(len(r.errors) for r in self.all),
        }
