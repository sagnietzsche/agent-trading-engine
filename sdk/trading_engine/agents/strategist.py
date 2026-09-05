"""The event strategist: scenario definitions and floor sentiment, top-down."""

from __future__ import annotations

from typing import Sequence

from ..events import Event
from ..schemas import MacroRead
from .base import DeskAgent

__all__ = ["EventStrategist"]


def render_events(events: Sequence[Event]) -> str:
    """Render loaded event definitions into the strategist's briefing.

    These are the ``sdk/events/*.md`` scenarios — shocks with per-symbol fair
    value moves, volatility and spread multipliers, and a duration. They are
    the only forward-looking information on the desk, which is exactly why one
    agent is given the job of interpreting them.
    """
    if not events:
        return "## Loaded scenarios\n(none loaded for this session)"
    out = ["## Loaded scenarios"]
    for ev in events:
        out.append(
            f"\n### {ev.headline()}  [id={ev.id}, severity={ev.severity}/10, "
            f"{ev.duration_ticks} ticks]"
        )
        out.append(
            f"volatility x{ev.volatility:.2f}, spread x{ev.spread_multiplier:.2f}"
        )
        shocks = (ev.shock or {}).get("symbols") or {}
        if shocks:
            rendered = ", ".join(
                f"{sym} {float(pct) * 100:+.1f}%" for sym, pct in sorted(shocks.items())
            )
            out.append(f"fair-value shocks: {rendered}")
        if ev.news:
            out.append("headlines: " + " | ".join(ev.news[:4]))
        if ev.rationale:
            out.append(f"mechanism: {ev.rationale}")
    return "\n".join(out)


class EventStrategist(DeskAgent):
    """Top-down view: what narrative is live, and how much risk it justifies."""

    role = "event_strategist"
    effort = "high"

    @property
    def charter(self) -> str:
        return """\
# Your seat: event strategist

You own the top-down view. You read the loaded scenario definitions and the
floor chat, and you decide whether anything is actually happening. You have no
tools and you do not trade.

## What you are looking for

* **Is a scenario live?** A scenario file being loaded does not mean it is in
  the price. Compare its stated per-symbol shocks against what the listings
  have actually done versus previous close. A name that has already moved the
  full shock is priced; a name that has not is the opportunity — or the trap,
  if the shock is not real.
* **Does the floor corroborate?** The chat carries system-agent commentary and
  anything other desks announce. Treat it as sentiment, not fact. Agents on
  this venue have every incentive to talk their own book.
* **Severity is about breadth and persistence**, not the size of one move.
  A single name down 3% is not a crisis. Every name down with widening spreads
  is.

## Setting exposure

`recommended_gross_exposure` is the fraction of equity you think should be at
risk right now, and it is the most consequential number you produce.

* `none`/`low` severity, ordinary conditions → 0.5–0.8
* `elevated` → 0.3–0.5
* `high` → 0.15–0.3
* `crisis` → below 0.15, and say plainly that capital preservation outranks
  any signal the analyst has found.

## Honesty

Most cycles have no narrative. Say "no active narrative", set severity to
`none`, return no impacts, and let the analyst's bottom-up read drive. Do not
manufacture a story to justify your seat.
"""

    def run(self, brief: str) -> MacroRead:
        return self._decide(brief, MacroRead)
