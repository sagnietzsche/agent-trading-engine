"""Hard risk limits, enforced in code after the risk officer has spoken.

The risk officer is a language model, and a language model that is asked to
police itself is a suggestion, not a control. So the desk runs two layers:

1. the :class:`~trading_engine.agents.risk.RiskOfficer` agent, which reasons
   about *this* market and can shrink or veto a ticket for reasons no static
   rule would catch; then
2. :func:`enforce`, which is arithmetic. It cannot be argued with, it can only
   ever reduce a ticket, and it is the last thing that runs before an order
   reaches the exchange.

The split matters: judgement belongs to the model, authority belongs to code.
"""

from __future__ import annotations

from dataclasses import dataclass, field
from typing import Any, Sequence

__all__ = ["ApprovedTicket", "RiskLimits", "RiskState", "enforce"]


@dataclass(frozen=True)
class RiskLimits:
    """The desk's mandate, as numbers.

    Every fraction is of current equity unless noted.
    """

    max_gross_exposure: float = 0.85
    """Total position market value / equity."""

    max_position_pct: float = 0.30
    """Market value of any single name / equity."""

    max_order_notional_pct: float = 0.15
    """Notional of any single order / equity — caps fat fingers."""

    max_orders_per_cycle: int = 6
    """Ticket count per cycle. Keeps one bad read from becoming ten trades."""

    min_cash_buffer_pct: float = 0.10
    """Free cash the desk refuses to spend, so it can always cover a stop."""

    max_drawdown_pct: float = 0.20
    """Peak-to-trough equity loss that stops the desk trading for the session."""

    def describe(self) -> str:
        return (
            f"gross exposure <= {self.max_gross_exposure:.0%} of equity; "
            f"any single name <= {self.max_position_pct:.0%}; "
            f"any single order <= {self.max_order_notional_pct:.0%}; "
            f"at most {self.max_orders_per_cycle} orders per cycle; "
            f"keep >= {self.min_cash_buffer_pct:.0%} of equity in cash; "
            f"session halts at a {self.max_drawdown_pct:.0%} drawdown"
        )


@dataclass
class RiskState:
    """Session-scoped risk tracking: the peak equity and the kill switch."""

    peak_equity: float = 0.0
    halted: bool = False
    halt_reason: str = ""

    def observe(self, equity: float, limits: RiskLimits) -> None:
        self.peak_equity = max(self.peak_equity, equity)
        if self.peak_equity <= 0.0 or self.halted:
            return
        drawdown = 1.0 - equity / self.peak_equity
        if drawdown >= limits.max_drawdown_pct:
            self.halted = True
            self.halt_reason = (
                f"session drawdown {drawdown:.1%} reached the "
                f"{limits.max_drawdown_pct:.0%} limit (peak ${self.peak_equity:,.0f})"
            )

    def drawdown(self, equity: float) -> float:
        """Current peak-to-trough loss as a fraction of the session peak."""
        if self.peak_equity <= 0.0:
            return 0.0
        return max(0.0, 1.0 - equity / self.peak_equity)


@dataclass
class ApprovedTicket:
    """One order the execution trader is authorized to work.

    ``qty`` is a ceiling, not an instruction: the trader may fill less, slice
    it, or skip it, but the execution tools reject anything beyond it.
    """

    symbol: str
    side: str
    qty: int
    order_type: str
    limit_price: float | None
    urgency: str
    rationale: str
    filled: int = field(default=0)

    @property
    def remaining(self) -> int:
        return max(0, self.qty - self.filled)


def _mark(marks: dict[str, float], symbol: str) -> float:
    return float(marks.get(symbol) or 0.0)


def enforce(
    proposals: Sequence[Any],
    verdicts: Sequence[Any],
    desk: dict[str, Any],
    marks: dict[str, float],
    limits: RiskLimits,
    state: RiskState,
) -> tuple[list[ApprovedTicket], list[str]]:
    """Clamp the PM's plan to what the mandate actually permits.

    ``proposals`` are :class:`~trading_engine.schemas.ProposedTrade` values and
    ``verdicts`` the risk officer's rulings, matched by position. Returns the
    approved tickets plus a human-readable list of every reduction and refusal,
    which is what gets written to the journal and printed to the operator.
    """
    rejections: list[str] = []
    equity = float(desk.get("equity") or 0.0)
    free_cash = float(desk.get("free_cash") or 0.0)
    positions = {p["symbol"]: p for p in desk.get("positions", [])}

    state.observe(equity, limits)
    if state.halted:
        return [], [f"HALT: {state.halt_reason} — no orders sent"]
    if equity <= 0.0:
        return [], ["HALT: desk equity is zero or unknown — no orders sent"]

    gross = sum(abs(float(p.get("value") or 0.0)) for p in positions.values())
    cash_floor = equity * limits.min_cash_buffer_pct
    spendable = max(0.0, free_cash - cash_floor)

    by_symbol_value = {s: abs(float(p.get("value") or 0.0)) for s, p in positions.items()}
    approved: list[ApprovedTicket] = []

    for i, trade in enumerate(proposals):
        label = f"{trade.side} {trade.qty} {trade.symbol}"

        # 1. The risk officer's own ruling, treated as a ceiling.
        #
        # An unreviewed ticket is a rejected ticket. If the risk officer
        # returned fewer verdicts than there were trades, or a verdict whose
        # symbol/side does not line up with the trade it is supposed to be
        # ruling on, the safe reading is that this trade was not reviewed —
        # not that it was approved by default.
        if i >= len(verdicts):
            rejections.append(f"{label}: dropped — risk returned no verdict for it")
            continue
        verdict = verdicts[i]
        if verdict.symbol != trade.symbol or verdict.side != trade.side:
            rejections.append(
                f"{label}: dropped — risk verdict {i} is for "
                f"{verdict.side} {verdict.symbol}, not this ticket"
            )
            continue
        if verdict.decision == "reject":
            rejections.append(f"{label}: vetoed by risk — {verdict.reason}")
            continue
        qty = min(int(trade.qty), max(0, int(verdict.approved_qty)))
        if qty <= 0:
            rejections.append(f"{label}: risk approved zero — {verdict.reason}")
            continue
        if qty < trade.qty:
            rejections.append(f"{label}: cut to {qty} by risk — {verdict.reason}")

        # 2. Ticket count.
        if len(approved) >= limits.max_orders_per_cycle:
            rejections.append(f"{label}: dropped — {limits.max_orders_per_cycle}-order cycle cap")
            continue

        price = trade.limit_price or _mark(marks, trade.symbol)
        if price <= 0.0:
            rejections.append(f"{label}: dropped — no usable price for {trade.symbol}")
            continue

        if trade.side == "buy":
            # 3a. Per-order notional.
            max_order_qty = int(equity * limits.max_order_notional_pct / price)
            if max_order_qty < qty:
                rejections.append(
                    f"{label}: cut to {max_order_qty} — single-order cap "
                    f"{limits.max_order_notional_pct:.0%} of equity"
                )
                qty = max_order_qty
            # 3b. Concentration in this name.
            headroom = equity * limits.max_position_pct - by_symbol_value.get(trade.symbol, 0.0)
            max_conc_qty = int(max(0.0, headroom) / price)
            if max_conc_qty < qty:
                rejections.append(
                    f"{label}: cut to {max_conc_qty} — {trade.symbol} would exceed "
                    f"{limits.max_position_pct:.0%} of equity"
                )
                qty = max_conc_qty
            # 3c. Gross exposure.
            gross_headroom = equity * limits.max_gross_exposure - gross
            max_gross_qty = int(max(0.0, gross_headroom) / price)
            if max_gross_qty < qty:
                rejections.append(
                    f"{label}: cut to {max_gross_qty} — gross exposure cap "
                    f"{limits.max_gross_exposure:.0%}"
                )
                qty = max_gross_qty
            # 3d. Cash, keeping the buffer intact.
            max_cash_qty = int(spendable / price)
            if max_cash_qty < qty:
                rejections.append(
                    f"{label}: cut to {max_cash_qty} — would breach the "
                    f"{limits.min_cash_buffer_pct:.0%} cash buffer"
                )
                qty = max_cash_qty
        else:
            # Sells are bounded by unreserved inventory. The desk is long-only:
            # the exchange has no borrow, so a short is simply unfillable.
            free = int(positions.get(trade.symbol, {}).get("free") or 0)
            if free < qty:
                rejections.append(
                    f"{label}: cut to {free} — only {free} free {trade.symbol} on the book"
                )
                qty = free

        if qty <= 0:
            continue

        notional = qty * price
        if trade.side == "buy":
            spendable -= notional
            gross += notional
            by_symbol_value[trade.symbol] = by_symbol_value.get(trade.symbol, 0.0) + notional
        else:
            gross = max(0.0, gross - notional)
            by_symbol_value[trade.symbol] = max(0.0, by_symbol_value.get(trade.symbol, 0.0) - notional)

        approved.append(
            ApprovedTicket(
                symbol=trade.symbol,
                side=trade.side,
                qty=qty,
                order_type=trade.order_type,
                limit_price=trade.limit_price,
                urgency=trade.urgency,
                rationale=trade.rationale,
            )
        )

    return approved, rejections
