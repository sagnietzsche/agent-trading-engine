"""The briefing is the desk's prompt, so its shape is a correctness concern.

Two invariants are worth a test. The venue manual is the cached prefix shared
by every agent on every cycle, so it must not vary — a single timestamp in
there would silently drop the cache hit rate to zero and quietly multiply the
bill. And the market brief must render the awkward states (no prints, no bid,
an empty book) rather than crashing the cycle.
"""

from trading_engine.brief import desk_brief, market_brief, marks_from_frame, venue_manual

FRAME = {
    "seq": 7,
    "welfare": {"gini": 0.42, "metric": "gini", "regime": "neutral", "total_equity": 1e7},
    "stocks": [
        {"symbol": "NOVA", "fair": 184.2, "last_trade": 180.0, "prev_close": 184.2,
         "bid": 179.9, "ask": 180.2},
        {"symbol": "QNTM", "fair": 92.75, "last_trade": None, "prev_close": 92.75,
         "bid": None, "ask": None},
    ],
    "book": {"symbol": "NOVA", "bids": [[179.9, 100]], "asks": [[180.2, 40], [180.5, 90]]},
    "tape": [],
    "chat": [],
}


def test_venue_manual_is_byte_stable_across_calls():
    assert venue_manual("neutral") == venue_manual("neutral")


def test_venue_manual_describes_the_regime_it_was_built_for():
    neutral = venue_manual("neutral")
    solidarity = venue_manual("solidarity")
    assert "NEUTRAL regime" in neutral
    assert "SOLIDARITY regime" not in neutral
    assert "SOLIDARITY regime" in solidarity
    assert "NEUTRAL regime" not in solidarity
    assert neutral != solidarity


def test_venue_manual_carries_no_wall_clock():
    """A timestamp here would invalidate the cached prefix on every cycle."""
    manual = venue_manual("neutral")
    assert not any(token in manual for token in (":00", "20260", "UTC", "GMT"))


def test_market_brief_survives_a_dead_quiet_market():
    text = market_brief(FRAME, cycle=1)
    assert "cycle 1" in text and "frame seq 7" in text
    assert "nothing has crossed the spread" in text
    assert "floor is quiet" in text
    # A symbol with no quotes renders as em dashes rather than blowing up.
    assert "QNTM" in text


def test_market_brief_precomputes_the_arithmetic_a_model_would_fumble():
    text = market_brief(FRAME, cycle=1)
    assert "spr(bp)" in text          # spread in basis points, not raw prices
    assert "-2.28%" in text           # 180.0 / 184.2 - 1
    assert "depth imbalance" in text


def test_market_brief_handles_a_missing_book():
    text = market_brief({**FRAME, "book": None}, cycle=2)
    assert "(no book)" in text


def test_desk_brief_reports_flat_and_positioned_states():
    marks = marks_from_frame(FRAME)
    assert marks["NOVA"] == 180.0
    assert marks["QNTM"] == 92.75   # falls back to fair when nothing has printed

    flat = desk_brief({"equity": 1000.0, "cash": 1000.0, "free_cash": 1000.0}, marks)
    assert "positions: flat" in flat and "working orders: none" in flat

    held = desk_brief(
        {
            "equity": 100_000.0, "cash": 50_000.0, "free_cash": 40_000.0,
            "positions": [{"symbol": "NOVA", "qty": 100, "free": 80,
                           "mark": 180.0, "value": 18_000.0}],
            "open_orders": [{"id": 9, "side": "buy", "qty": 20, "filled": 0,
                             "symbol": "NOVA", "price": 179.0, "status": "open"}],
        },
        marks,
    )
    assert "18.0%" in held        # position as a share of equity
    assert "#9 buy 20/20 NOVA" in held
