"""End-to-end pipeline test with a fake exchange and stubbed agents.

The point is to exercise the wiring — briefing assembly, the parallel analysis
stage, the risk clamp, tool authorization, the journal — without a network or
an API key. Every model call is replaced by a canned structured answer, which
is exactly what the schema boundary between agents is for: if the contract
holds for a stub, it holds for the model.
"""

from trading_engine.agents.trader import _Session, _build_tools
from trading_engine.desk import DeskConfig, TradingDesk
from trading_engine.llm import DeskLLM, Ledger
from trading_engine.risk import ApprovedTicket
from trading_engine.schemas import (
    EventImpact,
    MacroRead,
    MarketRead,
    PortfolioPlan,
    ProposedTrade,
    RiskAssessment,
    SymbolSignal,
    TradeVerdict,
)

AGENT_ID = "11111111-1111-1111-1111-111111111111"


class FakeExchange:
    """The slice of TradingClient the desk actually touches."""

    def __init__(self, equity=100_000.0, free_cash=100_000.0, positions=()):
        self.base_url = "http://fake"
        self.orders = []
        self.cancelled = []
        self._equity = equity
        self._free_cash = free_cash
        self._positions = list(positions)

    def health(self):
        return {"status": "ok", "database": "connected", "regime": "neutral", "metric": "gini"}

    def snapshot(self, symbol="NOVA"):
        return {
            "seq": 42,
            "welfare": {"gini": 0.31, "metric": "gini", "regime": "neutral",
                        "total_equity": 16_000_000.0},
            "stocks": [
                {"symbol": "NOVA", "name": "Nova Dynamics", "fair": 184.20,
                 "last_trade": 182.00, "prev_close": 184.20, "bid": 181.90, "ask": 182.20},
                {"symbol": "QNTM", "name": "Quantum Foundry", "fair": 92.75,
                 "last_trade": None, "prev_close": 92.75, "bid": 92.60, "ask": 92.90},
            ],
            "book": {"symbol": "NOVA", "bids": [[181.90, 120], [181.60, 300]],
                     "asks": [[182.20, 90], [182.50, 260]]},
            "tape": [{"id": "t1", "symbol": "NOVA", "price": 182.0, "qty": 40}],
            "agents": [{"id": AGENT_ID, "name": "atlas"}],
            "chat": [{"name": "market_maker", "text": "two-sided as usual"}],
            "tournament": None,
        }

    def agent(self, agent_id):
        return {
            "id": agent_id, "name": "atlas", "cash": self._free_cash,
            "reserved_cash": 0.0, "free_cash": self._free_cash,
            "equity": self._equity, "positions": self._positions, "open_orders": [],
        }

    def create_agent(self, name):
        return {"agent_id": AGENT_ID, "name": name}

    def book(self, symbol, levels=10):
        return {"symbol": symbol, "bids": [[181.90, 120]], "asks": [[182.20, 90]]}

    def place_order(self, agent_id, symbol, side, kind, qty, price=None):
        oid = len(self.orders) + 1
        self.orders.append(
            {"agent_id": agent_id, "symbol": symbol, "side": side,
             "kind": kind, "qty": qty, "price": price}
        )
        return {
            "order": {"id": oid, "symbol": symbol, "side": side, "kind": kind,
                      "qty": qty, "filled": qty, "status": "filled", "price": price},
            "fills": [{"trade_id": f"f{oid}", "price": price or 182.20, "qty": qty}],
            "free_cash": self._free_cash,
        }

    def cancel_order(self, order_id, agent_id):
        self.cancelled.append(order_id)
        return {"status": "cancelled"}


class StubLLM(DeskLLM):
    """A DeskLLM that answers from a script instead of calling Claude."""

    def __init__(self, plan_trades=None, verdicts=None):
        self.ledger = Ledger()
        self.calls: list[str] = []
        self._plan_trades = plan_trades
        self._verdicts = verdicts

    def decide(self, *, role, manual, charter, brief, schema, effort=None, **kw):
        self.calls.append(role)
        # The manual must be byte-identical across roles — that is the whole
        # basis of the desk's cached prompt prefix.
        assert manual.startswith("# Venue manual"), role
        if schema is MarketRead:
            return MarketRead(
                regime="mean_reverting",
                breadth="one name",
                signals=[SymbolSignal(symbol="NOVA", direction="long", conviction=0.7,
                                      estimated_fair_value=184.20, horizon_ticks=10,
                                      thesis="mid 1.2% below fair with heavier bid depth")],
                notes="",
            )
        if schema is MacroRead:
            return MacroRead(
                active_narrative="no active narrative", severity="none",
                impacts=[EventImpact(symbol="NOVA", impact="neutral", rationale="n/a")],
                recommended_gross_exposure=0.6, notes="",
            )
        if schema is PortfolioPlan:
            trades = self._plan_trades if self._plan_trades is not None else [
                ProposedTrade(symbol="NOVA", side="buy", qty=50, order_type="limit",
                              limit_price=182.00, urgency="normal",
                              rationale="analyst long, 0.7 conviction")
            ]
            return PortfolioPlan(stance="risk_on", trades=trades, reasoning="fund the top signal")
        if schema is RiskAssessment:
            verdicts = self._verdicts if self._verdicts is not None else [
                TradeVerdict(symbol="NOVA", side="buy", decision="approve",
                             approved_qty=50, reason="within every limit")
            ]
            return RiskAssessment(overall="clear", verdicts=verdicts, commentary="clear")
        raise AssertionError(f"unexpected schema {schema}")

    def act(self, *, role, manual, charter, brief, tools, **kw):
        """Stand in for the tool runner: call each place tool once."""
        self.calls.append(role)
        by_name = {t.name: t for t in tools}
        assert "place_limit_order" in by_name
        by_name["read_order_book"].call({"symbol": "NOVA"})
        by_name["place_limit_order"].call(
            {"symbol": "NOVA", "side": "buy", "qty": 50, "limit_price": 182.00}
        )
        return "bought 50 NOVA on the bid"


def make_desk(exchange, llm, **overrides):
    config = DeskConfig(name="atlas", cycles=1, cycle_pause_s=0.0, **overrides)
    return TradingDesk(exchange, config, llm=llm, log=lambda m: None)


def test_full_cycle_runs_every_seat_and_sends_the_order():
    ex = FakeExchange()
    llm = StubLLM()
    record = make_desk(ex, llm).run_cycle(1)

    assert set(llm.calls[:2]) == {"market_analyst", "event_strategist"}
    assert llm.calls[2:] == ["portfolio_manager", "risk_officer", "execution_trader"]
    assert record.stance == "risk_on"
    assert record.approved == [
        {"symbol": "NOVA", "side": "buy", "qty": 50, "order_type": "limit", "urgency": "normal"}
    ]
    assert len(ex.orders) == 1 and ex.orders[0]["qty"] == 50
    assert record.orders_sent[0]["filled"] == 50
    assert record.errors == []


def test_dry_run_decides_everything_and_sends_nothing():
    ex = FakeExchange()
    llm = StubLLM()
    record = make_desk(ex, llm, dry_run=True).run_cycle(1)
    assert record.approved  # the pipeline still produced tickets
    assert ex.orders == []
    assert "execution_trader" not in llm.calls
    assert "dry run" in record.execution_note


def test_risk_halt_blocks_every_ticket():
    class HaltingLLM(StubLLM):
        def decide(self, *, schema, **kw):
            result = super().decide(schema=schema, **kw)
            if schema is RiskAssessment:
                return RiskAssessment(overall="halt", verdicts=result.verdicts,
                                      commentary="market data unusable")
            return result

    ex = FakeExchange()
    record = make_desk(ex, HaltingLLM()).run_cycle(1)
    assert ex.orders == []
    assert record.risk_overall == "halt"
    assert any("HALT" in r for r in record.rejections)


def test_oversized_plan_is_clamped_by_code_even_when_risk_approves_it():
    """The LLM risk officer waving through 100x is not enough to send 100x."""
    big = ProposedTrade(symbol="NOVA", side="buy", qty=5_000, order_type="limit",
                        limit_price=182.00, urgency="normal", rationale="conviction")
    nod = TradeVerdict(symbol="NOVA", side="buy", decision="approve",
                       approved_qty=5_000, reason="looks fine to me")
    ex = FakeExchange()
    record = make_desk(ex, StubLLM(plan_trades=[big], verdicts=[nod])).run_cycle(1)
    # 100k equity x 15% single-order cap / 182.00 = 82 shares.
    assert record.approved[0]["qty"] == 82
    assert any("single-order cap" in r for r in record.rejections)


def test_empty_plan_is_a_valid_cycle():
    ex = FakeExchange()
    record = make_desk(ex, StubLLM(plan_trades=[], verdicts=[])).run_cycle(1)
    assert record.approved == []
    assert ex.orders == []
    assert record.execution_note == "nothing approved"
    assert record.errors == []


def test_journal_recall_feeds_the_next_cycle():
    ex = FakeExchange()
    desk = make_desk(ex, StubLLM())
    desk.run_cycle(1)
    recall = desk.journal.recall(101_000.0)
    assert "cycle 1" in recall
    assert "buy 50 NOVA" in recall


# -- execution authorization -------------------------------------------------


def session_with(*tickets):
    return _Session(FakeExchange(), AGENT_ID, list(tickets))


def tools_of(session):
    return {t.name: t for t in _build_tools(session)}


def test_trader_cannot_trade_a_symbol_it_was_not_given():
    s = session_with(ApprovedTicket("NOVA", "buy", 100, "limit", 182.0, "normal", "why"))
    out = tools_of(s)["place_limit_order"].call(
        {"symbol": "HELX", "side": "buy", "qty": 10, "limit_price": 340.0}
    )
    assert "not authorized" in out
    assert s.client.orders == []


def test_trader_cannot_flip_the_side_of_a_ticket():
    s = session_with(ApprovedTicket("NOVA", "buy", 100, "limit", 182.0, "normal", "why"))
    out = tools_of(s)["place_market_order"].call({"symbol": "NOVA", "side": "sell", "qty": 10})
    assert "not authorized" in out
    assert s.client.orders == []


def test_trader_cannot_exceed_the_authorized_quantity():
    s = session_with(ApprovedTicket("NOVA", "buy", 100, "limit", 182.0, "normal", "why"))
    out = tools_of(s)["place_limit_order"].call(
        {"symbol": "NOVA", "side": "buy", "qty": 101, "limit_price": 182.0}
    )
    assert "exceeds" in out
    assert s.client.orders == []


def test_slicing_within_a_ticket_is_allowed_until_it_is_worked():
    ticket = ApprovedTicket("NOVA", "buy", 100, "limit", 182.0, "normal", "why")
    s = session_with(ticket)
    tools = tools_of(s)
    for _ in range(2):
        tools["place_limit_order"].call(
            {"symbol": "NOVA", "side": "buy", "qty": 50, "limit_price": 182.0}
        )
    assert len(s.client.orders) == 2
    assert ticket.remaining == 0

    out = tools["place_limit_order"].call(
        {"symbol": "NOVA", "side": "buy", "qty": 1, "limit_price": 182.0}
    )
    assert "fully worked" in out
    assert len(s.client.orders) == 2


def test_denials_are_recorded_for_the_journal():
    s = session_with(ApprovedTicket("NOVA", "buy", 10, "limit", 182.0, "normal", "why"))
    tools_of(s)["place_limit_order"].call(
        {"symbol": "QNTM", "side": "buy", "qty": 1, "limit_price": 90.0}
    )
    assert len(s.result.denied) == 1
    assert "QNTM" in s.result.denied[0]


def test_drawdown_is_tracked_even_on_a_cycle_that_fails_early():
    """A failed cycle still saw the equity; the kill switch must not miss it."""
    from trading_engine.llm import LLMError
    from trading_engine.risk import RiskLimits

    class BrokenAnalyst(StubLLM):
        def decide(self, *, role, **kw):
            if role == "market_analyst":
                raise LLMError("upstream is down")
            return super().decide(role=role, **kw)

    ex = FakeExchange(equity=100_000.0)
    desk = make_desk(ex, BrokenAnalyst(), limits=RiskLimits(max_drawdown_pct=0.10))
    record = desk.run_cycle(1)
    assert record.errors and not desk.risk_state.halted

    ex._equity = 80_000.0
    desk.run_cycle(2)
    assert desk.risk_state.halted
    assert "drawdown" in desk.risk_state.halt_reason
