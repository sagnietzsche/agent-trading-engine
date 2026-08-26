"""Market events — load and validate event definitions from ``sdk/events``.

An event definition describes what a significant real-world shock (a global
recession, a war-like situation, an armageddon) does to the simulated market:
fair-value shocks per symbol, volatility, spread, liquidity, circuit
breakers, and how the solidarity machinery should behave while it runs.

Events live in ``sdk/events/*.md`` (a scenario doc with a JSON definition
embedded in a ```json code fence) or as bare ``*.json`` files. The loader
extracts and validates the JSON; the definition itself is the contract that
any engine integration would apply.
"""

from __future__ import annotations

import json
import re
from dataclasses import dataclass, field
from pathlib import Path
from typing import Any, Dict, List, Optional, Union

__all__ = ["Event", "EventError", "extract_json_block", "load_event", "load_events"]


class EventError(ValueError):
    """Raised when an event definition is malformed or fails validation."""


def extract_json_block(text: str) -> str:
    """Return the first fenced ```json ... ``` block in a document."""
    m = re.search(r"```json\s*\n(.*?)```", text, re.DOTALL)
    if not m:
        raise EventError("no fenced ```json block found in document")
    return m.group(1)


def _as_str(raw: Dict[str, Any], key: str, where: str) -> str:
    v = raw.get(key)
    if not isinstance(v, str) or not v.strip():
        raise EventError(f"{where}.{key}: expected a non-empty string, got {v!r}")
    return v.strip()


def _as_int(raw: Dict[str, Any], key: str, where: str, lo: int, hi: int) -> int:
    v = raw.get(key)
    if not isinstance(v, int) or isinstance(v, bool) or not (lo <= v <= hi):
        raise EventError(f"{where}.{key}: expected an integer in [{lo}, {hi}], got {v!r}")
    return v


def _as_float(raw: Dict[str, Any], key: str, where: str, lo: float, hi: float) -> float:
    v = raw.get(key)
    if not isinstance(v, (int, float)) or isinstance(v, bool) or not (lo <= v <= hi):
        raise EventError(f"{where}.{key}: expected a number in [{lo}, {hi}], got {v!r}")
    return float(v)


@dataclass
class Event:
    """A validated market event definition.

    Only the fields documented in ``sdk/events/README.md`` are accepted;
    anything else is rejected so typos surface at load time instead of
    silently doing nothing.
    """

    id: str
    name: str
    severity: int
    kind: str
    duration_ticks: int
    rationale: str = ""
    news: List[str] = field(default_factory=list)
    shock: Dict[str, Any] = field(default_factory=dict)
    drift: Dict[str, float] = field(default_factory=dict)
    volatility: float = 1.0
    spread_multiplier: float = 1.0
    liquidity: Dict[str, Any] = field(default_factory=dict)
    circuit_breaker: Dict[str, Any] = field(default_factory=dict)
    solidarity: Dict[str, float] = field(default_factory=dict)

    @classmethod
    def from_dict(cls, raw: Dict[str, Any]) -> "Event":
        if not isinstance(raw, dict):
            raise EventError(f"event definition must be a JSON object, got {type(raw).__name__}")

        known = {
            "id", "name", "severity", "kind", "duration_ticks", "rationale", "news",
            "shock", "drift", "volatility", "spread_multiplier", "liquidity",
            "circuit_breaker", "solidarity",
        }
        unknown = set(raw) - known
        if unknown:
            raise EventError(f"unknown event fields: {', '.join(sorted(unknown))}")

        ev = cls(
            id=_as_str(raw, "id", "event"),
            name=_as_str(raw, "name", "event"),
            severity=_as_int(raw, "severity", "event", 1, 10),
            kind=_as_str(raw, "kind", "event"),
            duration_ticks=_as_int(raw, "duration_ticks", "event", 1, 10_000_000),
            rationale=_as_str(raw, "rationale", "event") if raw.get("rationale") else "",
        )

        news = raw.get("news", [])
        if not isinstance(news, list) or not all(isinstance(n, str) for n in news):
            raise EventError("event.news: expected a list of strings")
        ev.news = list(news)

        ev.volatility = _as_float(raw, "volatility", "event", 1.0, 1_000.0) if "volatility" in raw else 1.0
        ev.spread_multiplier = _as_float(raw, "spread_multiplier", "event", 1.0, 1_000.0) if "spread_multiplier" in raw else 1.0

        ev.shock = cls._parse_shock(raw.get("shock"))
        ev.drift = cls._parse_drift(raw.get("drift"))
        ev.liquidity = cls._parse_liquidity(raw.get("liquidity"))
        ev.circuit_breaker = cls._parse_circuit_breaker(raw.get("circuit_breaker"))
        ev.solidarity = cls._parse_solidarity(raw.get("solidarity"))
        return ev

    @staticmethod
    def _parse_shock(raw: Any) -> Dict[str, Any]:
        if raw is None:
            return {}
        if not isinstance(raw, dict):
            raise EventError("event.shock: expected an object")
        symbols = raw.get("symbols", {})
        if not isinstance(symbols, dict) or not all(
            isinstance(k, str) and isinstance(v, (int, float)) and not isinstance(v, bool) and -1.0 <= v <= 1.0
            for k, v in symbols.items()
        ):
            raise EventError("event.shock.symbols: expected a map of symbol -> move in [-1, 1]")
        out: Dict[str, Any] = {"symbols": {k: float(v) for k, v in symbols.items()}}
        if "ticks" in raw:
            out["ticks"] = _as_int(raw, "ticks", "event.shock", 1, 10_000_000)
        if "decay" in raw:
            out["decay"] = _as_float(raw, "decay", "event.shock", 0.0, 1.0)
        return out

    @staticmethod
    def _parse_drift(raw: Any) -> Dict[str, float]:
        if raw is None:
            return {}
        if not isinstance(raw, dict) or not all(
            isinstance(k, str) and isinstance(v, (int, float)) and not isinstance(v, bool) and -1.0 <= v <= 1.0
            for k, v in raw.items()
        ):
            raise EventError("event.drift: expected a map of symbol -> per-tick drift in [-1, 1]")
        return {k: float(v) for k, v in raw.items()}

    @staticmethod
    def _parse_liquidity(raw: Any) -> Dict[str, Any]:
        if raw is None:
            return {}
        if not isinstance(raw, dict):
            raise EventError("event.liquidity: expected an object")
        out: Dict[str, Any] = {}
        if "levels" in raw:
            out["levels"] = _as_int(raw, "levels", "event.liquidity", 0, 100)
        if "size_multiplier" in raw:
            v = _as_float(raw, "size_multiplier", "event.liquidity", 0.0, 1.0)
            if v <= 0.0:
                raise EventError("event.liquidity.size_multiplier: expected a number in (0, 1]")
            out["size_multiplier"] = v
        return out

    @staticmethod
    def _parse_circuit_breaker(raw: Any) -> Dict[str, Any]:
        if raw is None:
            return {}
        if not isinstance(raw, dict):
            raise EventError("event.circuit_breaker: expected an object")
        out: Dict[str, Any] = {}
        if "drop_pct" in raw:
            out["drop_pct"] = _as_float(raw, "drop_pct", "event.circuit_breaker", 0.001, 1.0)
        if "halt_ticks" in raw:
            out["halt_ticks"] = _as_int(raw, "halt_ticks", "event.circuit_breaker", 0, 10_000_000)
        return out

    @staticmethod
    def _parse_solidarity(raw: Any) -> Dict[str, float]:
        if raw is None:
            return {}
        if not isinstance(raw, dict):
            raise EventError("event.solidarity: expected an object")
        out: Dict[str, float] = {}
        if "gini_target_multiplier" in raw:
            out["gini_target_multiplier"] = _as_float(raw, "gini_target_multiplier", "event.solidarity", 0.01, 100.0)
        if "gift_rate_multiplier" in raw:
            out["gift_rate_multiplier"] = _as_float(raw, "gift_rate_multiplier", "event.solidarity", 0.01, 100.0)
        return out

    def to_dict(self) -> Dict[str, Any]:
        return {
            "id": self.id,
            "name": self.name,
            "severity": self.severity,
            "kind": self.kind,
            "duration_ticks": self.duration_ticks,
            "rationale": self.rationale,
            "news": list(self.news),
            "shock": dict(self.shock),
            "drift": dict(self.drift),
            "volatility": self.volatility,
            "spread_multiplier": self.spread_multiplier,
            "liquidity": dict(self.liquidity),
            "circuit_breaker": dict(self.circuit_breaker),
            "solidarity": dict(self.solidarity),
        }

    def headline(self) -> str:
        """One-line summary of the event's market impact, for logs/UI."""
        parts = [f"{self.name} (severity {self.severity}/10, {self.kind})"]
        if self.shock:
            symbols = self.shock.get("symbols", {})
            if symbols:
                worst = min(symbols, key=symbols.get)
                best = max(symbols, key=symbols.get)
                parts.append(f"shock {worst} {symbols[worst]:+.0%} .. {best} {symbols[best]:+.0%}")
        parts.append(f"{self.duration_ticks} ticks")
        parts.append(f"volatility x{self.volatility:g}")
        parts.append(f"spread x{self.spread_multiplier:g}")
        if self.liquidity:
            parts.append(f"liquidity {self.liquidity.get('levels', '?')} levels x{self.liquidity.get('size_multiplier', 1):g}")
        return " · ".join(parts)


def load_event(path: Union[str, Path]) -> Event:
    """Load and validate one event definition from a ``.json`` file or a
    ``.md`` scenario document with an embedded ```json block."""
    p = Path(path)
    if not p.exists():
        raise EventError(f"event file not found: {p}")
    text = p.read_text(encoding="utf-8")
    if p.suffix.lower() == ".json":
        raw_text = text
    else:
        raw_text = extract_json_block(text)
    try:
        raw = json.loads(raw_text)
    except json.JSONDecodeError as exc:
        raise EventError(f"{p}: invalid JSON: {exc}") from exc
    return Event.from_dict(raw)


def load_events(directory: Union[str, Path] = None) -> List[Event]:
    """Load every event definition under a directory (``sdk/events`` by
    default), sorted by file name. `README.md` and other non-event docs in
    the directory are skipped."""
    d = Path(directory) if directory is not None else Path(__file__).resolve().parent.parent / "events"
    if not d.is_dir():
        raise EventError(f"event directory not found: {d}")
    files = [p for p in sorted(d.glob("*.md")) if p.name != "README.md"] + sorted(d.glob("*.json"))
    events: List[Event] = []
    for p in files:
        try:
            events.append(load_event(p))
        except EventError as exc:
            raise EventError(f"{p}: {exc}") from exc
    return events
