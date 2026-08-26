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
from .events import Event, EventError, load_event, load_events
from .ws import WatchStream

__all__ = [
    "Agent",
    "Context",
    "Event",
    "EventError",
    "GreedyMomentumStrategy",
    "load_event",
    "load_events",
    "MandateStrategy",
    "OrderIntent",
    "Strategy",
    "TradingClient",
    "TradingError",
    "WatchStream",
]

__version__ = "0.1.0"
