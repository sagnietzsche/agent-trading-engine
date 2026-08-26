"""Live WebSocket feed for /api/ws with reconnect + resubscribe."""

from __future__ import annotations

import json
import threading
from typing import Any, Callable

from websocket import WebSocketApp  # type: ignore[import-untyped]

FrameCallback = Callable[[dict[str, Any]], None]


def _ws_url(base_url: str) -> str:
    base = base_url.rstrip("/")
    if base.startswith("https://"):
        return base[: len("https://")] + "//" + base[len("https://") :] + "/api/ws"
    return "ws" + base[len("http") :] + "/api/ws"


class WatchStream:
    """Subscribe to live market frames.

        stream = WatchStream("http://127.0.0.1:8080", symbol="NOVA",
                             agent_id=agent_id, on_frame=print)
        stream.start()
        ...
        stream.close()

Frames arrive on a background thread; keep the callback fast and thread-safe.
"""

    def __init__(
        self,
        base_url: str,
        symbol: str = "NOVA",
        agent_id: str | None = None,
        on_frame: FrameCallback | None = None,
        on_status: Callable[[str], None] | None = None,
    ) -> None:
        self.url = _ws_url(base_url)
        self.symbol = symbol
        self.agent_id = agent_id
        self.on_frame = on_frame or (lambda frame: None)
        self.on_status = on_status or (lambda status: None)
        self._sock: WebSocketApp | None = None
        self._lock = threading.Lock()
        self._closing = False

    def start(self) -> WatchStream:
        self._connect()
        return self

    def _connect(self) -> None:
        sock = WebSocketApp(
            self.url,
            on_open=self._on_open,
            on_message=self._on_message,
            on_error=self._on_error,
            on_close=self._on_close,
        )
        with self._lock:
            self._sock = sock
        threading.Thread(target=sock.run_forever, daemon=True).start()

    def update_subscription(self, symbol: str | None = None, agent_id: str | None = None) -> None:
        if symbol is not None:
            self.symbol = symbol
        if agent_id is not None:
            self.agent_id = agent_id
        self._send(
            {"type": "subscribe", "symbol": self.symbol, "agent_id": self.agent_id}
        )

    def ping(self) -> None:
        self._send({"type": "ping"})

    def close(self) -> None:
        self._closing = True
        with self._lock:
            sock = self._sock
        if sock is not None:
            try:
                sock.close()
            except Exception:  # noqa: BLE001
                pass

    # -- internals ------------------------------------------------------------

    def _send(self, payload: dict[str, Any]) -> None:
        with self._lock:
            sock = self._sock
        if sock is not None and sock.sock is not None:
            try:
                sock.send(json.dumps(payload))
            except Exception:  # noqa: BLE001
                pass

    def _on_open(self, _: WebSocketApp) -> None:
        self.on_status("open")
        self.update_subscription(self.symbol, self.agent_id)

    def _on_message(self, _: WebSocketApp, message: str) -> None:
        try:
            frame = json.loads(message)
        except ValueError:
            return
        kind = frame.get("type")
        if kind == "snapshot" or kind not in ("subscribed", "pong"):
            self.on_frame(frame)
        elif kind == "subscribed":
            print(f"[watch] subscribed to {frame.get('symbol')}")

    def _on_error(self, _: WebSocketApp, err: Any) -> None:
        self.on_status(f"error: {err}")

    def _on_close(self, _: WebSocketApp, *_args: Any) -> None:
        self.on_status("closed")
        if not self._closing:
            threading.Timer(1.5, self._reconnect).start()

    def _reconnect(self) -> None:
        if not self._closing:
            self.on_status("reconnecting")
            self._connect()
