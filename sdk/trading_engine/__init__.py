"""trading-engine-sdk: build agents for the solidarity mock exchange."""

from .agent import (
    Agent,
    Context,
    GreedyMomentumStrategy,
    MandateStrategy,
    OrderIntent,
    Strategy,
)
from .client import TradingClient, TradingError
from .ws import WatchStream

__all__ = [
    "Agent",
    "Context",
    "GreedyMomentumStrategy",
    "MandateStrategy",
    "OrderIntent",
    "Strategy",
    "TradingClient",
    "TradingError",
    "WatchStream",
]

__version__ = "0.1.0"
