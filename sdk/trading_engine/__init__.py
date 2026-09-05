"""trading-engine-sdk: an LLM agent desk for the mock exchange.

The desk is five Claude agents with one job each — analyst, event strategist,
portfolio manager, risk officer, execution trader — wired together by typed
contracts, bounded by hard limits enforced in code, and logged to a replayable
journal. :class:`TradingDesk` runs the whole pipeline; the individual agents
and schemas are exported so you can swap a seat or run one in isolation.
"""

from .agents import (
    DeskAgent,
    EventStrategist,
    ExecutionResult,
    ExecutionTrader,
    MarketAnalyst,
    PortfolioManager,
    RiskOfficer,
)
from .brief import desk_brief, market_brief, venue_manual
from .client import TradingClient, TradingError
from .desk import DeskConfig, TradingDesk
from .events import Event, EventError, load_event, load_events
from .journal import CycleRecord, Journal
from .llm import DeskLLM, Ledger, LLMError, ModelPolicy, Refusal
from .risk import ApprovedTicket, RiskLimits, RiskState, enforce
from .schemas import (
    MacroRead,
    MarketRead,
    PortfolioPlan,
    ProposedTrade,
    RiskAssessment,
    SymbolSignal,
    TradeVerdict,
)
from .ws import WatchStream

__all__ = [
    "ApprovedTicket",
    "CycleRecord",
    "DeskAgent",
    "DeskConfig",
    "DeskLLM",
    "Event",
    "EventError",
    "EventStrategist",
    "ExecutionResult",
    "ExecutionTrader",
    "Journal",
    "Ledger",
    "LLMError",
    "MacroRead",
    "MarketAnalyst",
    "MarketRead",
    "ModelPolicy",
    "PortfolioManager",
    "PortfolioPlan",
    "ProposedTrade",
    "Refusal",
    "RiskAssessment",
    "RiskLimits",
    "RiskOfficer",
    "RiskState",
    "SymbolSignal",
    "TradeVerdict",
    "TradingClient",
    "TradingDesk",
    "TradingError",
    "WatchStream",
    "desk_brief",
    "enforce",
    "load_event",
    "load_events",
    "market_brief",
    "venue_manual",
]

__version__ = "0.2.0"
