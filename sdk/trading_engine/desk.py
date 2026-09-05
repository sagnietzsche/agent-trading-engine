"""The orchestrator: one cycle of the desk, run end to end.

This is the loop the whole project exists to demonstrate. Each cycle:

1. **Observe.** Pull a live frame and our own desk state, render both as a
   compact briefing.
2. **Analyse — in parallel.** The market analyst and the event strategist look
   at the same briefing from different directions and have nothing to say to
   each other, so they run concurrently. Two API calls, one wall-clock wait.
3. **Decide.** The portfolio manager reads both, plus the session journal, and
   produces a basket of orders.
4. **Constrain.** The risk officer rules on the basket; then
   :func:`~trading_engine.risk.enforce` clamps it again in code. Judgement,
   then arithmetic.
5. **Execute.** The trader — the only role with tools — works the surviving
   tickets into the book.
6. **Record.** The whole cycle goes to the journal, and a summary of it comes
   back as context on the next one.

Failures are contained per cycle. A refusal, a rate limit, or a malformed read
loses that cycle's decision and is written to the journal; it does not take
the session down. A trading desk that stops trading because one call failed is
worse than one that sits out a minute.
"""

from __future__ import annotations

import time
from concurrent.futures import ThreadPoolExecutor
from dataclasses import dataclass, field
from pathlib import Path
from typing import Any, Callable, Sequence

from .agents import (
    EventStrategist,
    ExecutionTrader,
    MarketAnalyst,
    PortfolioManager,
    RiskOfficer,
)
from .agents.strategist import render_events
from .brief import desk_brief, market_brief, marks_from_frame, venue_manual
from .client import TradingClient, TradingError
from .events import Event
from .journal import CycleRecord, Journal
from .llm import DeskLLM, LLMError, ModelPolicy, Refusal
from .risk import RiskLimits, RiskState, enforce

__all__ = ["DeskConfig", "TradingDesk"]


@dataclass
class DeskConfig:
    """Everything about a desk that is a choice rather than a discovery."""

    name: str = "atlas"
    symbol: str = "NOVA"
    cycles: int = 5
    cycle_pause_s: float = 2.0
    limits: RiskLimits = field(default_factory=RiskLimits)
    journal_path: Path | None = None
    events: Sequence[Event] = ()
    dry_run: bool = False
    model: str = "claude-opus-5"


class TradingDesk:
    """A team of LLM agents trading one account on the exchange."""

    def __init__(
        self,
        client: TradingClient,
        config: DeskConfig | None = None,
        llm: DeskLLM | None = None,
        log: Callable[[str], None] | None = None,
    ) -> None:
        self.client = client
        self.config = config or DeskConfig()
        self.log = log or (lambda msg: print(msg, flush=True))
        self.llm = llm or DeskLLM(policy=ModelPolicy(model=self.config.model))
        self.journal = Journal(self.config.journal_path)
        self.risk_state = RiskState()
        self.agent_id: str | None = None

        regime = self._detect_regime()
        self.manual = venue_manual(regime)
        self.regime = regime

        self.analyst = MarketAnalyst(self.llm, self.manual)
        self.strategist = EventStrategist(self.llm, self.manual)
        self.pm = PortfolioManager(self.llm, self.manual)
        self.risk = RiskOfficer(self.llm, self.manual)
        self.trader = ExecutionTrader(self.llm, self.manual)

    # -- setup --------------------------------------------------------------

    def _detect_regime(self) -> str:
        """Ask the venue which microstructure it is running.

        The manual is the desk's cached prompt prefix, so this is resolved once
        at construction: changing it mid-session would invalidate the cache on
        every agent.
        """
        try:
            return str(self.client.health().get("regime") or "neutral")
        except TradingError:
            return "neutral"

    def register(self) -> str:
        """Find or create this desk's account on the exchange."""
        if self.agent_id:
            return self.agent_id
        for row in self.client.snapshot(self.config.symbol).get("agents", []):
            if row.get("name") == self.config.name:
                self.agent_id = row["id"]
                return self.agent_id
        self.agent_id = self.client.create_agent(self.config.name)["agent_id"]
        return self.agent_id

    # -- one cycle ----------------------------------------------------------

    def run_cycle(self, cycle: int) -> CycleRecord:
        started = time.monotonic()
        agent_id = self.register()
        record = CycleRecord(cycle=cycle)

        frame = self.client.snapshot(self.config.symbol)
        desk = self.client.agent(agent_id)
        marks = marks_from_frame(frame)
        record.seq = frame.get("seq")
        record.equity = float(desk.get("equity") or 0.0)
        # Track drawdown here, not only inside enforce(): a cycle that fails
        # before it reaches the limit checker still happened, and the kill
        # switch should not be blind to the equity it saw on the way past.
        self.risk_state.observe(record.equity, self.config.limits)

        mkt = market_brief(frame, cycle)
        mine = desk_brief(desk, marks)
        scenarios = render_events(list(self.config.events))

        # 2. Analyst and strategist are independent; overlap their latency.
        with ThreadPoolExecutor(max_workers=2) as pool:
            f_market = pool.submit(self.analyst.run, mkt)
            f_macro = pool.submit(self.strategist.run, f"{mkt}\n\n{scenarios}")
            try:
                market_read = f_market.result()
                macro_read = f_macro.result()
            except (LLMError, Refusal) as err:
                record.errors.append(f"analysis: {err}")
                record.wall_seconds = time.monotonic() - started
                self.journal.append(record)
                self.log(f"  cycle {cycle}: analysis failed — {err}")
                return record

        record.regime_read = market_read.regime
        record.narrative = macro_read.active_narrative
        record.severity = macro_read.severity
        self.log(
            f"  analyst: {market_read.regime}, {len(market_read.signals)} signal(s) | "
            f"strategist: {macro_read.severity} — {macro_read.active_narrative} "
            f"(target exposure {macro_read.recommended_gross_exposure:.0%})"
        )

        # 3. The PM decides.
        try:
            plan = self.pm.run(
                mkt, mine, market_read, macro_read,
                self.journal.recall(record.equity), self.config.limits,
            )
        except (LLMError, Refusal) as err:
            record.errors.append(f"pm: {err}")
            record.wall_seconds = time.monotonic() - started
            self.journal.append(record)
            self.log(f"  cycle {cycle}: PM failed — {err}")
            return record

        record.stance = plan.stance
        record.proposed = [t.model_dump() for t in plan.trades]
        self.log(f"  pm: {plan.stance}, {len(plan.trades)} proposed trade(s)")

        # 4a. The risk officer rules. A failure here is not a reason to trade
        #     unchecked — it is a reason to sit out.
        if plan.trades:
            try:
                assessment = self.risk.run(
                    mkt, mine, plan, self.config.limits, self.risk_state, record.equity
                )
            except (LLMError, Refusal) as err:
                record.errors.append(f"risk: {err} — cycle skipped, nothing sent")
                record.wall_seconds = time.monotonic() - started
                self.journal.append(record)
                self.log(f"  cycle {cycle}: risk unavailable — standing down")
                return record
            record.risk_overall = assessment.overall
            verdicts = [] if assessment.overall == "halt" else assessment.verdicts
            if assessment.overall == "halt":
                record.rejections.append(f"risk HALT: {assessment.commentary}")
        else:
            record.risk_overall = "clear"
            verdicts = []

        # 4b. Then arithmetic, which cannot be talked around.
        tickets, rejections = enforce(
            plan.trades if record.risk_overall != "halt" else [],
            verdicts, desk, marks, self.config.limits, self.risk_state,
        )
        record.rejections.extend(rejections)
        record.approved = [
            {"symbol": t.symbol, "side": t.side, "qty": t.qty,
             "order_type": t.order_type, "urgency": t.urgency}
            for t in tickets
        ]
        for line in rejections:
            self.log(f"  risk/limits: {line}")

        # 5. Execute.
        if not tickets:
            record.execution_note = "nothing approved"
        elif self.config.dry_run:
            record.execution_note = "dry run — tickets approved but no orders sent"
            self.log(f"  execution: DRY RUN, {len(tickets)} ticket(s) withheld")
        else:
            try:
                result = self.trader.run(self.client, agent_id, tickets, mkt, mine)
            except (LLMError, Refusal) as err:
                record.errors.append(f"execution: {err}")
                record.wall_seconds = time.monotonic() - started
                self.journal.append(record)
                self.log(f"  cycle {cycle}: execution failed — {err}")
                return record
            record.orders_sent = result.orders_sent
            record.execution_note = result.note
            record.rejections.extend(result.denied)
            self.log(
                f"  execution: {len(result.orders_sent)} order(s) sent, "
                f"{result.filled_qty} shares filled"
            )
            if result.note:
                self.log(f"  trader: {result.note.splitlines()[0][:160]}")

        record.wall_seconds = time.monotonic() - started
        self.journal.append(record)
        return record

    # -- session ------------------------------------------------------------

    def run(self) -> dict[str, Any]:
        """Run the configured number of cycles and return a session summary."""
        agent_id = self.register()
        opening = self.client.agent(agent_id)
        start_equity = float(opening.get("equity") or 0.0)
        self.log(
            f"desk '{self.config.name}' [{agent_id}] on a {self.regime} venue — "
            f"opening equity ${start_equity:,.2f}, {self.config.cycles} cycle(s)"
        )
        self.log(f"mandate: {self.config.limits.describe()}")

        for cycle in range(1, self.config.cycles + 1):
            self.log(f"\n── cycle {cycle}/{self.config.cycles} ──")
            record = self.run_cycle(cycle)
            self.log(f"  equity ${record.equity:,.2f} · {record.wall_seconds:.1f}s")
            if self.risk_state.halted:
                self.log(f"\nDESK HALTED: {self.risk_state.halt_reason}")
                break
            if cycle < self.config.cycles:
                time.sleep(self.config.cycle_pause_s)

        closing = self.client.agent(agent_id)
        end_equity = float(closing.get("equity") or 0.0)
        summary = {
            **self.journal.summary(),
            "agent_id": agent_id,
            "start_equity": start_equity,
            "end_equity": end_equity,
            "return_pct": (end_equity / start_equity - 1.0) if start_equity else 0.0,
            "halted": self.risk_state.halted,
            "cost_usd": self.llm.ledger.total_cost_usd,
        }
        self.log(
            f"\nsession over — equity ${start_equity:,.0f} → ${end_equity:,.0f} "
            f"({summary['return_pct'] * 100:+.2f}%), "
            f"{summary['orders_sent']} order(s), "
            f"{summary['risk_interventions']} risk intervention(s)"
        )
        self.log("\n" + self.llm.ledger.table())
        return summary
