"""The desk's connection to Claude: model policy, caching, and a cost ledger.

Every agent goes through :class:`DeskLLM` rather than touching the Anthropic
client directly, which buys three things worth having on a trading desk:

* **One model policy.** Effort is set per role, not per call site — the analyst
  thinks hard, the execution trader does not need to.
* **A cache-shaped prompt.** The venue manual is identical for every agent and
  every cycle, so it goes in the first system block behind a cache breakpoint;
  the volatile market brief goes in the user turn, after it. Cache hit rate is
  measured, not assumed — :attr:`Ledger.cache_hit_rate` reports it.
* **A bill.** Multi-agent pipelines get expensive quietly. Every call is
  metered per role so a run ends with a per-agent cost breakdown.
"""

from __future__ import annotations

import threading
import time
from dataclasses import dataclass, field
from typing import Any, Sequence, TypeVar

import anthropic
from pydantic import BaseModel

__all__ = ["DeskLLM", "Ledger", "LLMError", "ModelPolicy", "Refusal", "RoleUsage"]

T = TypeVar("T", bound=BaseModel)

# Opus 5 is the desk's default: these are judgement calls under uncertainty
# with real (simulated) money attached, which is where the capability gap
# actually shows up.
DEFAULT_MODEL = "claude-opus-5"

# Server-side refusal fallbacks. A policy decline mid-run would otherwise
# silently drop a cycle's decision; with this the API re-runs the request on a
# fallback model inside the same call.
FALLBACK_BETA = "server-side-fallback-2026-07-01"


class LLMError(RuntimeError):
    """A call to Claude failed in a way the desk cannot route around."""


class Refusal(LLMError):
    """The whole model chain declined the request; this cycle has no decision."""


@dataclass(frozen=True)
class ModelPolicy:
    """How much thinking one role gets.

    ``effort`` trades tokens for depth. Research roles run high; the execution
    trader is following orders, not forming a view, so it runs low and cheap.
    """

    model: str = DEFAULT_MODEL
    effort: str = "high"
    max_tokens: int = 16_000

    def with_effort(self, effort: str) -> "ModelPolicy":
        return ModelPolicy(self.model, effort, self.max_tokens)


# Published Claude API rates, USD per million tokens. Used for the run's cost
# estimate only — the API's own billing is authoritative.
_PRICES: dict[str, tuple[float, float]] = {
    "claude-opus-5": (5.00, 25.00),
    "claude-opus-4-8": (5.00, 25.00),
    "claude-sonnet-5": (3.00, 15.00),
    "claude-haiku-4-5": (1.00, 5.00),
}


@dataclass
class RoleUsage:
    """Running token/cost totals for one agent role."""

    calls: int = 0
    input_tokens: int = 0
    output_tokens: int = 0
    cache_write_tokens: int = 0
    cache_read_tokens: int = 0
    seconds: float = 0.0
    refusals: int = 0

    def cost(self, model: str) -> float:
        inp, out = _PRICES.get(model, _PRICES[DEFAULT_MODEL])
        # Cache reads bill at a fraction of the input rate; cache writes at a
        # premium. Approximated here at the documented 0.1x / 1.25x.
        billable_in = self.input_tokens + 0.1 * self.cache_read_tokens + 1.25 * self.cache_write_tokens
        return (billable_in * inp + self.output_tokens * out) / 1_000_000


@dataclass
class Ledger:
    """Per-role usage for a whole desk session, priced against one model."""

    model: str = DEFAULT_MODEL
    roles: dict[str, RoleUsage] = field(default_factory=dict)
    _lock: threading.Lock = field(default_factory=threading.Lock, repr=False)

    def record(self, role: str, usage: Any, seconds: float, refused: bool = False) -> None:
        with self._lock:
            row = self.roles.setdefault(role, RoleUsage())
            row.calls += 1
            row.seconds += seconds
            if refused:
                row.refusals += 1
            if usage is None:
                return
            row.input_tokens += getattr(usage, "input_tokens", 0) or 0
            row.output_tokens += getattr(usage, "output_tokens", 0) or 0
            row.cache_write_tokens += getattr(usage, "cache_creation_input_tokens", 0) or 0
            row.cache_read_tokens += getattr(usage, "cache_read_input_tokens", 0) or 0

    @property
    def total_cost_usd(self) -> float:
        return sum(r.cost(self.model) for r in self.roles.values())

    @property
    def total_calls(self) -> int:
        return sum(r.calls for r in self.roles.values())

    @property
    def cache_hit_rate(self) -> float:
        """Share of prompt tokens served from cache.

        A number near zero across many cycles means something volatile crept
        into the cached prefix — the usual suspect is a timestamp.
        """
        read = sum(r.cache_read_tokens for r in self.roles.values())
        fresh = sum(r.input_tokens + r.cache_write_tokens for r in self.roles.values())
        total = read + fresh
        return read / total if total else 0.0

    def table(self) -> str:
        header = f"{'role':<22}{'calls':>6}{'in':>10}{'out':>9}{'cached':>10}{'s':>7}{'usd':>9}"
        lines = [header, "-" * len(header)]
        for name in sorted(self.roles):
            r = self.roles[name]
            lines.append(
                f"{name:<22}{r.calls:>6}{r.input_tokens:>10,}{r.output_tokens:>9,}"
                f"{r.cache_read_tokens:>10,}{r.seconds:>7.1f}{r.cost(self.model):>9.3f}"
            )
        lines.append("-" * len(header))
        lines.append(
            f"{'total':<22}{self.total_calls:>6}{'':>10}{'':>9}"
            f"{self.cache_hit_rate * 100:>9.0f}%{'':>7}{self.total_cost_usd:>9.3f}"
        )
        return "\n".join(lines)


class DeskLLM:
    """Thin, opinionated wrapper over the Anthropic SDK for desk agents."""

    def __init__(
        self,
        *,
        client: anthropic.Anthropic | None = None,
        policy: ModelPolicy | None = None,
        ledger: Ledger | None = None,
        max_attempts: int = 3,
    ) -> None:
        # A bare client resolves credentials from ANTHROPIC_API_KEY, then
        # ANTHROPIC_AUTH_TOKEN, then an `ant auth login` profile — so an unset
        # API key does not mean there are no credentials.
        self.client = client or anthropic.Anthropic()
        self.policy = policy or ModelPolicy()
        self.ledger = ledger or Ledger(model=self.policy.model)
        self.max_attempts = max_attempts

    # -- prompt assembly ----------------------------------------------------

    @staticmethod
    def system_blocks(manual: str, charter: str) -> list[dict[str, Any]]:
        """Two system blocks with the cache breakpoint after the shared manual.

        The manual is byte-identical for every agent and every cycle, so it is
        the longest stable prefix the desk has. The role charter follows it
        uncached because it differs per agent — putting the breakpoint after
        the manual lets all five roles share one cached prefix.
        """
        return [
            {"type": "text", "text": manual, "cache_control": {"type": "ephemeral"}},
            {"type": "text", "text": charter},
        ]

    # -- calls --------------------------------------------------------------

    def decide(
        self,
        *,
        role: str,
        manual: str,
        charter: str,
        brief: str,
        schema: type[T],
        effort: str | None = None,
        max_tokens: int | None = None,
    ) -> T:
        """Run one agent turn that must answer in ``schema``.

        Structured output means the caller gets a validated Pydantic instance
        or an exception — never prose it has to guess at.
        """
        started = time.monotonic()
        last: Exception | None = None
        for attempt in range(self.max_attempts):
            try:
                response = self.client.beta.messages.parse(
                    model=self.policy.model,
                    max_tokens=max_tokens or self.policy.max_tokens,
                    system=self.system_blocks(manual, charter),
                    messages=[{"role": "user", "content": brief}],
                    output_format=schema,
                    output_config={"effort": effort or self.policy.effort},
                    thinking={"type": "adaptive"},
                    betas=[FALLBACK_BETA],
                    fallbacks="default",
                )
            except (anthropic.RateLimitError, anthropic.APIConnectionError, anthropic.InternalServerError) as err:
                last = err
                time.sleep(min(2 ** attempt, 8))
                continue
            except anthropic.APIStatusError as err:
                self.ledger.record(role, None, time.monotonic() - started)
                raise LLMError(f"{role}: {err}") from err

            if response.stop_reason == "refusal":
                self.ledger.record(role, response.usage, time.monotonic() - started, refused=True)
                raise Refusal(f"{role}: model chain declined the request")
            self.ledger.record(role, response.usage, time.monotonic() - started)
            parsed = response.parsed_output
            if parsed is None:
                raise LLMError(f"{role}: structured output missing from response")
            return parsed
        raise LLMError(f"{role}: giving up after {self.max_attempts} attempts") from last

    def act(
        self,
        *,
        role: str,
        manual: str,
        charter: str,
        brief: str,
        tools: Sequence[Any],
        effort: str | None = None,
        max_iterations: int = 8,
        max_tokens: int | None = None,
    ) -> str:
        """Run one agent turn that acts on the world through ``tools``.

        The SDK's tool runner drives the request → execute → loop cycle; the
        tools themselves are where authorization lives, so a runaway model can
        only ever call something it was already permitted to call.
        """
        started = time.monotonic()
        runner = self.client.beta.messages.tool_runner(
            model=self.policy.model,
            max_tokens=max_tokens or self.policy.max_tokens,
            system=self.system_blocks(manual, charter),
            messages=[{"role": "user", "content": brief}],
            tools=list(tools),
            max_iterations=max_iterations,
            output_config={"effort": effort or "low"},
            thinking={"type": "adaptive"},
            betas=[FALLBACK_BETA],
            fallbacks="default",
        )
        final = runner.until_done()
        self.ledger.record(role, getattr(final, "usage", None), time.monotonic() - started)
        if getattr(final, "stop_reason", None) == "refusal":
            raise Refusal(f"{role}: model chain declined the request")
        return "\n".join(b.text for b in final.content if b.type == "text").strip()


def credentials_available(client: anthropic.Anthropic | None = None) -> bool:
    """Report whether the SDK found credentials, before a run burns a cycle on it.

    An unset ``ANTHROPIC_API_KEY`` is not proof of no credentials: the SDK also
    reads ``ANTHROPIC_AUTH_TOKEN`` and the profile written by ``ant auth
    login``. Constructing a client is not proof either — the constructor
    succeeds with nothing configured and only fails at request time with a 401.
    So ask the constructed client what it actually resolved.
    """
    try:
        c = client or anthropic.Anthropic()
    except Exception:  # noqa: BLE001 - a construction failure means no creds
        return False
    return any(
        getattr(c, attr, None) for attr in ("api_key", "auth_token", "credentials")
    )
