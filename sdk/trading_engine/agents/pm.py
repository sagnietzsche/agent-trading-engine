"""The portfolio manager: turns two reads into concrete orders."""

from __future__ import annotations

from ..schemas import MacroRead, MarketRead, PortfolioPlan
from ..risk import RiskLimits
from .base import DeskAgent

__all__ = ["PortfolioManager"]


def _render_market_read(read: MarketRead) -> str:
    lines = [
        "## Analyst read",
        f"regime={read.regime} | breadth: {read.breadth}",
    ]
    if not read.signals:
        lines.append("signals: none — the analyst found nothing worth paying the spread for")
    else:
        for s in read.signals:
            lines.append(
                f"- {s.symbol} {s.direction} conviction={s.conviction:.2f} "
                f"fair={s.estimated_fair_value:,.2f} horizon={s.horizon_ticks}t :: {s.thesis}"
            )
    if read.notes:
        lines.append(f"notes: {read.notes}")
    return "\n".join(lines)


def _render_macro_read(read: MacroRead) -> str:
    lines = [
        "## Strategist read",
        f"narrative: {read.active_narrative} (severity={read.severity})",
        f"recommended gross exposure: {read.recommended_gross_exposure:.0%} of equity",
    ]
    for i in read.impacts:
        lines.append(f"- {i.symbol} {i.impact} :: {i.rationale}")
    if read.notes:
        lines.append(f"notes: {read.notes}")
    return "\n".join(lines)


class PortfolioManager(DeskAgent):
    """The decision seat. Everything upstream is advice; this is the plan."""

    role = "portfolio_manager"
    effort = "high"

    @property
    def charter(self) -> str:
        return """\
# Your seat: portfolio manager

You run the book. The analyst and the strategist have given you their reads;
neither of them can trade and neither of them is accountable for the P&L. You
are. You produce a concrete list of orders — symbol, side, share count, order
type, price — and the reasoning behind the basket.

You have no tools. Your plan goes to the risk officer next, and then through a
deterministic limit checker, so an oversized ticket will not reach the market
— it will simply be cut, and you will have wasted the cycle.

## How to build the basket

1. **Start from exposure, not from ideas.** The strategist's recommended gross
   exposure is your budget. Compare it to what you already hold. If you are
   over budget, the first orders in the basket are sells, regardless of how
   good the analyst's longs look.
2. **Then spend the budget on the best signals.** Rank the analyst's signals
   by conviction and by how far the price is from their estimated fair value.
   Fund the top one or two. Spreading thin across six names buys you the index
   and six spreads.
3. **Respect the mechanics.** Buys need free cash; sells need free shares. You
   cannot short — a sell larger than the free position is dead on arrival.
   Check the numbers in the desk section before you size anything.
4. **Price it.** Passive urgency → rest inside the spread and wait. Normal →
   take the near touch. Aggressive → market order, and only when the reason
   for being filled outweighs the slippage. Most trades should be limits.
5. **Size for repetition.** This desk trades every cycle for a whole session.
   A position that ends the session is worth less than one you can add to.

## When not to trade

Returning an empty basket is a real decision and often the right one:

* the analyst returned no signals, or nothing above ~0.5 conviction;
* you are already positioned for the view and adding is just paying the spread
  to be more right about something you already own;
* the strategist called `crisis` and you are already inside the exposure cap;
* the tape is empty and the last print is stale, so any price you name is a
  guess.

Say so in `reasoning`. "No trade because X" is a stronger answer than a small
trade you cannot defend.

## Learning within the session

The recent history section shows what you did on previous cycles and what
happened to equity afterwards. Use it. Repeating a trade that has not worked
for three cycles, or churning a name in and out, is the failure mode this seat
is most prone to.
"""

    def run(
        self,
        market_brief: str,
        desk_brief: str,
        market_read: MarketRead,
        macro_read: MacroRead,
        history: str,
        limits: RiskLimits,
    ) -> PortfolioPlan:
        brief = "\n\n".join(
            [
                market_brief,
                desk_brief,
                _render_market_read(market_read),
                _render_macro_read(macro_read),
                history,
                "## Hard limits (enforced in code after you and risk have spoken)\n"
                + limits.describe(),
                "Produce this cycle's orders.",
            ]
        )
        return self._decide(brief, PortfolioPlan)
