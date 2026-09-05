"""Typed decision contracts between the desk's agents.

Every agent on this desk is a language model, so the only thing keeping the
pipeline from degenerating into prose telephone is a schema at each seam. Each
model here is the *output contract* of exactly one role, handed to the Claude
API as a structured-output format so the response is validated before the next
agent ever sees it. If an agent drifts, it fails at the boundary rather than
three steps later inside the matching engine.

The seams, in pipeline order::

    MarketRead      analyst      what the tape and the book are saying
    MacroRead       strategist   what the loaded events and the floor imply
    PortfolioPlan   PM           what we should therefore do
    RiskAssessment  risk officer what we are actually allowed to do
"""

from __future__ import annotations

from typing import List, Literal, Optional

from pydantic import BaseModel, Field

__all__ = [
    "EventImpact",
    "MacroRead",
    "MarketRead",
    "PortfolioPlan",
    "ProposedTrade",
    "RiskAssessment",
    "SymbolSignal",
    "TradeVerdict",
]

Direction = Literal["long", "short", "flat"]
Side = Literal["buy", "sell"]


# -- analyst ----------------------------------------------------------------


class SymbolSignal(BaseModel):
    """The analyst's read on one listing."""

    symbol: str = Field(description="Ticker, e.g. NOVA")
    direction: Direction = Field(
        description="long if the fair value sits above the market, short if below, "
        "flat if there is no edge worth paying the spread for"
    )
    conviction: float = Field(
        ge=0.0, le=1.0, description="0 = noise, 1 = the clearest signal on the board"
    )
    estimated_fair_value: float = Field(
        gt=0.0, description="Where this should trade once the imbalance clears"
    )
    horizon_ticks: int = Field(
        ge=1, le=120, description="How many ticks the edge is expected to persist"
    )
    thesis: str = Field(
        max_length=400,
        description="One or two sentences citing the specific book, tape, or price "
        "evidence behind the call",
    )


class MarketRead(BaseModel):
    """The market analyst's whole-board view for one cycle."""

    regime: Literal["trending", "mean_reverting", "volatile", "quiet"]
    breadth: str = Field(max_length=200, description="Is the move broad or one name?")
    signals: List[SymbolSignal] = Field(
        description="One entry per listing you have an opinion on. Omit names you "
        "cannot justify — an empty list is a valid, honest answer."
    )
    notes: str = Field(max_length=600, description="Anything the PM should know")


# -- macro / event strategist ------------------------------------------------


class EventImpact(BaseModel):
    symbol: str
    impact: Literal[
        "severe_negative", "negative", "neutral", "positive", "severe_positive"
    ]
    rationale: str = Field(max_length=300)


class MacroRead(BaseModel):
    """The event strategist's view: scenario definitions and floor sentiment."""

    active_narrative: str = Field(
        max_length=300,
        description="What is driving the tape right now, in one line. Say 'no active "
        "narrative' when nothing is.",
    )
    severity: Literal["none", "low", "elevated", "high", "crisis"]
    impacts: List[EventImpact] = Field(
        description="Only symbols the narrative actually touches."
    )
    recommended_gross_exposure: float = Field(
        ge=0.0,
        le=1.0,
        description="Fraction of equity that should be at risk given the narrative. "
        "Crisis conditions call for a low number.",
    )
    notes: str = Field(max_length=600)


# -- portfolio manager -------------------------------------------------------


class ProposedTrade(BaseModel):
    """One order the PM wants working. Quantities are shares, never notional."""

    symbol: str
    side: Side
    qty: int = Field(gt=0, description="Shares. Must be affordable from free cash "
                                       "(buys) or free shares (sells).")
    order_type: Literal["limit", "market"]
    limit_price: Optional[float] = Field(
        default=None,
        description="Required for limit orders, omitted for market orders.",
    )
    urgency: Literal["passive", "normal", "aggressive"] = Field(
        description="How hard the execution trader should chase the fill."
    )
    rationale: str = Field(
        max_length=400,
        description="Which analyst or event signal this trade expresses.",
    )


class PortfolioPlan(BaseModel):
    """The PM's orders for this cycle — before risk has had a say."""

    stance: Literal["risk_on", "neutral", "risk_off"]
    trades: List[ProposedTrade] = Field(
        description="Empty is a legitimate plan. Do not trade to look busy."
    )
    reasoning: str = Field(
        max_length=900,
        description="Why this basket, and what would make you change your mind.",
    )


# -- risk officer ------------------------------------------------------------


class TradeVerdict(BaseModel):
    """A ruling on one proposed trade. Risk may shrink a ticket, never grow it."""

    symbol: str
    side: Side
    decision: Literal["approve", "reduce", "reject"]
    approved_qty: int = Field(
        ge=0,
        description="Shares permitted. Must be <= the proposed qty; 0 for a reject.",
    )
    reason: str = Field(max_length=300)


class RiskAssessment(BaseModel):
    """The risk officer's ruling on the PM's whole plan."""

    overall: Literal["clear", "caution", "halt"] = Field(
        description="halt blocks every trade this cycle regardless of the verdicts."
    )
    verdicts: List[TradeVerdict] = Field(
        description="Exactly one verdict per proposed trade, in the same order."
    )
    commentary: str = Field(
        max_length=600, description="The binding constraint, named explicitly."
    )
