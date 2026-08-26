"""Strategies and the runner that turns live frames into orders."""

from __future__ import annotations

import abc
import time
from dataclasses import dataclass, field
from typing import Any

from .client import TradingClient
from .ws import WatchStream


@dataclass
class OrderIntent:
    symbol: str
    side: str  # "buy" | "sell"
    qty: int
    kind: str = "limit"  # "limit" | "market"
    price: float | None = None


@dataclass
class Context:
    """Everything a strategy may look at for one tick."""

    client: TradingClient
    agent_id: str
    frame: dict[str, Any]
    desk: dict[str, Any]
    welfare: dict[str, Any]
    mandate: dict[str, Any] | None
    tournament: dict[str, Any] | None
    _intents: list[OrderIntent] = field(default_factory=list)

    @property
    def suggestion(self) -> dict[str, Any] | None:
        return (self.mandate or {}).get("suggestion")

    def submit(self, intent: OrderIntent) -> None:
        self._intents.append(intent)

    def _drain(self) -> list[OrderIntent]:
        out = self._intents
        self._intents = []
        return out


class Strategy(abc.ABC):
    """Implement on_tick(); return intents or ctx.submit() them."""

    name: str = "custom"

    @abc.abstractmethod
    def on_tick(self, ctx: Context) -> list[OrderIntent] | None:
        ...


class MandateStrategy(Strategy):
    """The cooperative reference bot: obey the welfare mandate verbatim."""

    name = "mandate"

    def on_tick(self, ctx: Context) -> list[OrderIntent] | None:
        s = ctx.suggestion
        if not s:
            return []
        return [OrderIntent(s["symbol"], s["side"], int(s["qty"]), "limit", float(s["limit"]))]


class GreedyMomentumStrategy(Strategy):
    """The foil: chase momentum, never read the mandate.

    Market-buys symbols that dipped more than 0.5% below prev_close and
    market-sells holdings that ran more than 0.5% above it.
    """

    name = "greedy"

    def __init__(self, clip_qty: int = 10) -> None:
        self.clip_qty = clip_qty

    def on_tick(self, ctx: Context) -> list[OrderIntent] | None:
        intents: list[OrderIntent] = []
        free_cash = float(ctx.desk.get("free_cash", 0.0))
        positions = {p["symbol"]: p for p in ctx.desk.get("positions", [])}
        for stock in ctx.frame.get("stocks", []):
            last = stock.get("last_trade")
            if last is None:
                continue
            change = last / stock["prev_close"] - 1.0
            sym = stock["symbol"]
            pos = positions.get(sym)
            held = int(pos.get("free", 0)) if pos else 0
            if change < -0.005 and free_cash > last * self.clip_qty:
                intents.append(OrderIntent(sym, "buy", self.clip_qty, "market"))
                free_cash -= last * self.clip_qty
            elif change > 0.005 and held > 0:
                intents.append(OrderIntent(sym, "sell", min(held, self.clip_qty), "market"))
        return intents


STRATEGIES = {"mandate": MandateStrategy, "greedy": GreedyMomentumStrategy}


class Agent:
    """Binds an agent id to a client and runs strategies against live frames."""

    def __init__(self, client: TradingClient, agent_id: str, name: str) -> None:
        self.client = client
        self.agent_id = agent_id
        self.name = name
        self.orders_sent = 0

    @classmethod
    def create(cls, client: TradingClient, name: str) -> Agent:
        created = client.create_agent(name)
        return cls(client, created["agent_id"], name)

    def submit(self, intent: OrderIntent) -> dict:
        result = self.client.place_order(
            self.agent_id,
            intent.symbol,
            intent.side,
            intent.kind,
            intent.qty,
            intent.price,
        )
        self.orders_sent += 1
        return result

    def run(
        self,
        strategy: Strategy,
        duration_s: float = 60.0,
        symbol: str = "NOVA",
        use_ws: bool = True,
        log: Any = None,
    ) -> dict[str, Any]:
        """Drive `strategy` from live frames (WS) or polling until duration elapses."""
        printer = log or (lambda msg: print(msg, flush=True))
        deadline = time.monotonic() + duration_s
        stats: dict[str, Any] = {"ticks": 0, "orders": 0}

        def handle_frame(frame: dict[str, Any]) -> None:
            if time.monotonic() > deadline:
                return
            desk = self.client.agent(self.agent_id)
            mandates = frame.get("mandates") or []
            mandate = next((m for m in mandates if m.get("agent_id") == self.agent_id), None)
            ctx = Context(
                client=self.client,
                agent_id=self.agent_id,
                frame=frame,
                desk=desk,
                welfare=frame.get("welfare", {}),
                mandate=mandate,
                tournament=frame.get("tournament"),
            )
            returned = strategy.on_tick(ctx) or []
            intents = ctx._drain() + returned
            rejected = 0
            for intent in intents:
                try:
                    self.submit(intent)
                    stats["orders"] += 1
                except Exception as err:  # noqa: BLE001
                    rejected += 1
                    printer(f"  order rejected: {err}")
            stats["ticks"] += 1
            role = (mandate or {}).get("role", "?")
            t = frame.get("tournament")
            tourney = f" | {t['name']}:{t['status']}({t.get('ticks_left')}t)" if t else ""
            printer(
                f"[{strategy.name}] tick={stats['ticks']} equity=${desk.get('equity', 0):,.0f} "
                f"role={role} orders={stats['orders']}"
                + (f" rejected={rejected}" if rejected else "")
                + tourney
            )

        stream: WatchStream | None = None
        try:
            if use_ws:
                stream = WatchStream(
                    self.client.base_url,
                    symbol=symbol,
                    agent_id=self.agent_id,
                    on_frame=handle_frame,
                ).start()
            while time.monotonic() < deadline:
                if stream is None:
                    snap = self.client.snapshot(symbol)
                    handle_frame({**snap, "mandates": self.client.welfare()["agents"]})
                    time.sleep(1.2)
                else:
                    time.sleep(0.25)
        finally:
            if stream is not None:
                stream.close()
        return stats
