"""The deterministic limit checker is the desk's last line of defence.

These tests exist because this is the one layer that must hold when the models
misbehave — an LLM risk officer that approves a 90%-of-equity ticket is a
plausible failure, and enforce() is what makes it harmless.
"""

from types import SimpleNamespace


from trading_engine.risk import ApprovedTicket, RiskLimits, RiskState, enforce


def trade(symbol="NOVA", side="buy", qty=100, order_type="limit", price=100.0,
          urgency="normal", rationale="test"):
    return SimpleNamespace(
        symbol=symbol, side=side, qty=qty, order_type=order_type,
        limit_price=price, urgency=urgency, rationale=rationale,
    )


def verdict(symbol="NOVA", side="buy", decision="approve", qty=100, reason="ok"):
    return SimpleNamespace(
        symbol=symbol, side=side, decision=decision, approved_qty=qty, reason=reason
    )


def desk(equity=100_000.0, free_cash=100_000.0, positions=()):
    return {
        "equity": equity,
        "cash": free_cash,
        "free_cash": free_cash,
        "positions": list(positions),
    }


MARKS = {"NOVA": 100.0, "QNTM": 50.0}


def test_clean_ticket_passes_through_untouched():
    limits = RiskLimits()
    tickets, rejections = enforce(
        [trade(qty=100)], [verdict(qty=100)], desk(), MARKS, limits, RiskState()
    )
    assert len(tickets) == 1
    assert tickets[0].qty == 100
    assert rejections == []


def test_risk_officer_can_only_shrink_a_ticket():
    """A verdict that approves MORE than proposed must not enlarge the order."""
    tickets, _ = enforce(
        [trade(qty=50)], [verdict(qty=5_000)], desk(), MARKS, RiskLimits(), RiskState()
    )
    assert tickets[0].qty == 50


def test_single_order_notional_cap_bites():
    # 100k equity, 15% cap, $100 mark -> 150 shares max.
    tickets, rejections = enforce(
        [trade(qty=1_000)], [verdict(qty=1_000)], desk(), MARKS, RiskLimits(), RiskState()
    )
    assert tickets[0].qty == 150
    assert any("single-order cap" in r for r in rejections)


def test_concentration_cap_accounts_for_the_existing_position():
    # Already 25% of equity in NOVA; the 30% cap leaves room for 50 shares.
    d = desk(positions=[{"symbol": "NOVA", "qty": 250, "free": 250, "mark": 100.0, "value": 25_000.0}])
    tickets, rejections = enforce(
        [trade(qty=140)], [verdict(qty=140)], d, MARKS, RiskLimits(), RiskState()
    )
    assert tickets[0].qty == 50
    assert any("exceed" in r for r in rejections)


def test_cash_buffer_is_never_spent():
    limits = RiskLimits(max_order_notional_pct=1.0, max_position_pct=1.0, max_gross_exposure=1.0)
    # 100k equity, 10% buffer -> 90k spendable -> 900 shares at $100.
    tickets, rejections = enforce(
        [trade(qty=1_000)], [verdict(qty=1_000)], desk(), MARKS, limits, RiskState()
    )
    assert tickets[0].qty == 900
    assert any("cash buffer" in r for r in rejections)


def test_sell_is_bounded_by_free_shares_because_there_is_no_borrow():
    d = desk(positions=[{"symbol": "NOVA", "qty": 40, "free": 10, "mark": 100.0, "value": 4_000.0}])
    tickets, rejections = enforce(
        [trade(side="sell", qty=40)], [verdict(side="sell", qty=40)],
        d, MARKS, RiskLimits(), RiskState(),
    )
    assert tickets[0].qty == 10
    assert any("free NOVA" in r for r in rejections)


def test_veto_drops_the_ticket_entirely():
    tickets, rejections = enforce(
        [trade()], [verdict(decision="reject", qty=0, reason="thesis unsupported")],
        desk(), MARKS, RiskLimits(), RiskState(),
    )
    assert tickets == []
    assert "thesis unsupported" in rejections[0]


def test_cycle_order_cap_drops_the_tail():
    limits = RiskLimits(max_orders_per_cycle=2)
    proposals = [trade(qty=10), trade(qty=10), trade(qty=10), trade(qty=10)]
    verdicts = [verdict(qty=10) for _ in proposals]
    tickets, rejections = enforce(proposals, verdicts, desk(), MARKS, limits, RiskState())
    assert len(tickets) == 2
    assert sum("cycle cap" in r for r in rejections) == 2


def test_drawdown_halts_the_session_and_stays_halted():
    state = RiskState()
    limits = RiskLimits(max_drawdown_pct=0.10)
    enforce([trade(qty=1)], [verdict(qty=1)], desk(equity=100_000.0), MARKS, limits, state)
    assert not state.halted

    tickets, rejections = enforce(
        [trade(qty=1)], [verdict(qty=1)], desk(equity=85_000.0), MARKS, limits, state
    )
    assert state.halted
    assert tickets == []
    assert rejections[0].startswith("HALT")

    # Recovering above the threshold does not un-halt the session.
    tickets, _ = enforce(
        [trade(qty=1)], [verdict(qty=1)], desk(equity=99_000.0), MARKS, limits, state
    )
    assert tickets == []


def test_no_usable_price_is_dropped_rather_than_guessed():
    tickets, rejections = enforce(
        [trade(symbol="ZZZZ", price=None)], [verdict(symbol="ZZZZ")],
        desk(), MARKS, RiskLimits(), RiskState(),
    )
    assert tickets == []
    assert "no usable price" in rejections[0]


def test_ticket_remaining_tracks_partial_work():
    t = ApprovedTicket("NOVA", "buy", 100, "limit", 100.0, "normal", "why")
    assert t.remaining == 100
    t.filled = 60
    assert t.remaining == 40
    t.filled = 200
    assert t.remaining == 0


def test_a_ticket_with_no_verdict_is_dropped_not_waved_through():
    """A short verdict list must not mean 'approved by default'."""
    proposals = [trade(qty=10), trade(symbol="QNTM", qty=10)]
    tickets, rejections = enforce(
        proposals, [verdict(qty=10)], desk(), MARKS, RiskLimits(), RiskState()
    )
    assert len(tickets) == 1 and tickets[0].symbol == "NOVA"
    assert any("no verdict" in r for r in rejections)


def test_a_misaligned_verdict_is_dropped():
    """Verdicts are matched by position; a mismatch means risk lost its place."""
    tickets, rejections = enforce(
        [trade(symbol="NOVA", side="buy")], [verdict(symbol="QNTM", side="sell")],
        desk(), MARKS, RiskLimits(), RiskState(),
    )
    assert tickets == []
    assert "not this ticket" in rejections[0]
