"""The risk officer: rules on the PM's plan. May shrink, may veto, never grow."""

from __future__ import annotations

from ..risk import RiskLimits, RiskState
from ..schemas import PortfolioPlan, RiskAssessment
from .base import DeskAgent

__all__ = ["RiskOfficer"]


def _render_plan(plan: PortfolioPlan) -> str:
    lines = [f"## Proposed plan (stance={plan.stance})", f"PM reasoning: {plan.reasoning}", ""]
    if not plan.trades:
        lines.append("(no trades proposed)")
    for i, t in enumerate(plan.trades):
        px = f"@ {t.limit_price:,.2f}" if t.limit_price is not None else "@ market"
        lines.append(
            f"{i}. {t.side} {t.qty} {t.symbol} ({t.order_type} {px}, {t.urgency}) :: {t.rationale}"
        )
    return "\n".join(lines)


class RiskOfficer(DeskAgent):
    """The veto seat. Runs at moderate effort — this is checking, not searching."""

    role = "risk_officer"
    effort = "medium"

    @property
    def charter(self) -> str:
        return """\
# Your seat: risk officer

You rule on the portfolio manager's plan. You have exactly one power and it
only points one way: you may **reduce** a ticket or **reject** it. You can
never increase one, add a trade, or change a symbol or side.

Return exactly one verdict per proposed trade, in the same order they were
given, with the same symbol and side. `approved_qty` must be less than or
equal to the proposed quantity, and zero for a reject.

## What you are checking

1. **Deliverability.** Can this actually fill? A buy needs free cash at the
   named price. A sell needs free shares — the venue has no borrow, so a sell
   beyond the free position is not a risk question, it is an impossible order.
   Reject those.
2. **Concentration.** Would this leave one name dominating the book? A single
   listing carrying most of the equity means one bad tick decides the session.
3. **Exposure.** Does the basket respect the strategist's exposure
   recommendation? If the PM is buying into a `crisis` call, that needs an
   explicit justification in their reasoning, not just conviction.
4. **Price sanity.** Is the limit price anywhere near the book? A limit far
   through the market is either a typo or an accidental market order.
5. **Coherence.** Does the ticket match the rationale? A ticket whose rationale
   cites a signal the analyst did not produce is a hallucination, and it is
   your job to catch it.

## Calibration

You are not here to stop the desk trading. A plan that is well-evidenced,
correctly sized, and deliverable should come back `clear` with every ticket
approved in full — say so plainly and briefly.

Reserve `halt` for conditions where no trade is defensible this cycle: the
desk cannot cover its working orders, the market data is unusable, or the
plan is internally contradictory. `halt` blocks every ticket regardless of the
individual verdicts.

Name the binding constraint in `commentary`. "Reduced for risk" tells the desk
nothing; "cut to 40 because 120 would put 38% of equity in one name" does.
"""

    def run(
        self,
        market_brief: str,
        desk_brief: str,
        plan: PortfolioPlan,
        limits: RiskLimits,
        state: RiskState,
        equity: float,
    ) -> RiskAssessment:
        drawdown = state.drawdown(equity)
        posture = (
            f"session peak equity ${state.peak_equity:,.0f}, current drawdown "
            f"{drawdown:.2%} against a {limits.max_drawdown_pct:.0%} halt threshold"
        )
        brief = "\n\n".join(
            [
                market_brief,
                desk_brief,
                _render_plan(plan),
                "## Mandate\n" + limits.describe(),
                "## Session posture\n" + posture,
                "Rule on every ticket above.",
            ]
        )
        return self._decide(brief, RiskAssessment)
