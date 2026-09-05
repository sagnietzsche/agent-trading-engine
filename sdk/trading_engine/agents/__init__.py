"""The five roles that make up the desk.

Each agent is a language model with one job, one system charter, and one
output contract. None of them shares state with another: information moves
between them only as validated :mod:`trading_engine.schemas` objects, in the
order the desk runs them. Only :class:`ExecutionTrader` holds tools that reach
the exchange.
"""

from .analyst import MarketAnalyst
from .base import DeskAgent
from .pm import PortfolioManager
from .risk import RiskOfficer
from .strategist import EventStrategist
from .trader import ExecutionTrader, ExecutionResult

__all__ = [
    "DeskAgent",
    "EventStrategist",
    "ExecutionResult",
    "ExecutionTrader",
    "MarketAnalyst",
    "PortfolioManager",
    "RiskOfficer",
]
