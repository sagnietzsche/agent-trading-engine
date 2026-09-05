"""Shared plumbing for the desk's agents."""

from __future__ import annotations

from typing import Any, TypeVar

from pydantic import BaseModel

from ..llm import DeskLLM

__all__ = ["DeskAgent"]

T = TypeVar("T", bound=BaseModel)


class DeskAgent:
    """Base class: a named role, an effort level, and a system charter.

    Subclasses supply ``role``, ``effort``, and a ``charter`` — the persona and
    decision policy for that seat. Everything else (caching, retries, the cost
    ledger, refusal handling) lives in :class:`~trading_engine.llm.DeskLLM`, so
    an agent file stays about the *job*, not the transport.
    """

    role: str = "agent"
    effort: str = "high"

    def __init__(self, llm: DeskLLM, manual: str) -> None:
        self.llm = llm
        self.manual = manual

    @property
    def charter(self) -> str:
        raise NotImplementedError

    def _decide(self, brief: str, schema: type[T], **kwargs: Any) -> T:
        return self.llm.decide(
            role=self.role,
            manual=self.manual,
            charter=self.charter,
            brief=brief,
            schema=schema,
            effort=self.effort,
            **kwargs,
        )
