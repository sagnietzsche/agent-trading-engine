"""Typed REST client for the trading-engine mock exchange."""

from __future__ import annotations

from typing import Any

import requests


class TradingError(RuntimeError):
    """Raised when the exchange answers with a non-2xx response."""


class TradingClient:
    def __init__(self, base_url: str = "http://127.0.0.1:8080", timeout: float = 5.0) -> None:
        self.base_url = base_url.rstrip("/")
        self.timeout = timeout
        self.session = requests.Session()

    def _req(self, method: str, path: str, *, json: Any = None, params: Any = None) -> Any:
        url = f"{self.base_url}{path}"
        resp = self.session.request(method, url, json=json, params=params, timeout=self.timeout)
        if not resp.ok:
            try:
                detail = resp.json().get("error", resp.text)
            except ValueError:
                detail = resp.text
            raise TradingError(f"{method} {path} -> {resp.status_code}: {detail}")
        return resp.json()

    # -- market data ----------------------------------------------------------

    def health(self) -> dict:
        return self._req("GET", "/api/health")

    def snapshot(self, symbol: str = "NOVA") -> dict:
        return self._req("GET", "/api/snapshot", params={"symbol": symbol})

    def welfare(self) -> dict:
        return self._req("GET", "/api/welfare")

    def stocks(self) -> list:
        return self._req("GET", "/api/stocks")

    def book(self, symbol: str, levels: int = 10) -> dict:
        return self._req("GET", f"/api/book/{symbol}", params={"levels": levels})

    def trades(self, limit: int = 50, symbol: str | None = None) -> list:
        params: dict[str, Any] = {"limit": limit}
        if symbol:
            params["symbol"] = symbol
        return self._req("GET", "/api/trades", params=params)

    # -- agents & orders --------------------------------------------------------

    def create_agent(self, name: str) -> dict:
        return self._req("POST", "/api/agents", json={"name": name})

    def agent(self, agent_id: str) -> dict:
        return self._req("GET", f"/api/agents/{agent_id}")

    def place_order(
        self,
        agent_id: str,
        symbol: str,
        side: str,
        kind: str = "limit",
        qty: int = 1,
        price: float | None = None,
    ) -> dict:
        body: dict[str, Any] = {
            "agent_id": agent_id,
            "symbol": symbol,
            "side": side,
            "kind": kind,
            "qty": qty,
        }
        if price is not None:
            body["price"] = price
        return self._req("POST", "/api/orders", json=body)

    def cancel_order(self, order_id: int, agent_id: str) -> dict:
        return self._req("DELETE", f"/api/orders/{order_id}", params={"agent_id": agent_id})

    # -- tournaments --------------------------------------------------------------

    def create_tournament(self, name: str, duration_ticks: int = 90) -> dict:
        return self._req(
            "POST", "/api/tournaments", json={"name": name, "duration_ticks": duration_ticks}
        )

    def tournaments(self) -> list:
        return self._req("GET", "/api/tournaments")

    def tournament(self, tournament_id: str) -> dict:
        return self._req("GET", f"/api/tournaments/{tournament_id}")

    def enter_tournament(self, tournament_id: str, agent_id: str, strategy: str = "custom") -> dict:
        return self._req(
            "POST",
            f"/api/tournaments/{tournament_id}/enter",
            json={"agent_id": agent_id, "strategy": strategy},
        )

    def start_tournament(self, tournament_id: str) -> dict:
        return self._req("POST", f"/api/tournaments/{tournament_id}/start")

    def reset_market(self) -> dict:
        """Wipe and reseed everything. Admin action."""
        return self._req("POST", "/api/admin/reset")
