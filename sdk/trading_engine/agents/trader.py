"""The execution trader — the only agent holding tools that reach the market.

Everyone upstream produces text. This agent produces orders, so it is the one
place where a bad model output becomes a real (simulated) fill. The design
follows from that: the trader is given a *ticket list* that already cleared
both the risk officer and the deterministic limit checker, and its tools
refuse anything the tickets do not authorize.

Authorization lives in the tool functions, not in the prompt. A prompt is a
request; a tool that checks its arguments before calling the exchange is a
control. If the model asks to sell a name that was never approved, the tool
returns an error the model can read and correct from — it does not reach the
exchange, and the loop does not crash.
"""

from __future__ import annotations

from dataclasses import dataclass, field
from typing import Any

from anthropic import beta_tool

from ..client import TradingClient, TradingError
from ..risk import ApprovedTicket
from .base import DeskAgent

__all__ = ["ExecutionResult", "ExecutionTrader"]


@dataclass
class ExecutionResult:
    """What the execution turn actually did."""

    orders_sent: list[dict[str, Any]] = field(default_factory=list)
    denied: list[str] = field(default_factory=list)
    note: str = ""

    @property
    def filled_qty(self) -> int:
        return sum(o.get("filled", 0) for o in self.orders_sent)


class _Session:
    """Per-cycle authorization state shared by the trader's tools."""

    def __init__(self, client: TradingClient, agent_id: str, tickets: list[ApprovedTicket]) -> None:
        self.client = client
        self.agent_id = agent_id
        self.tickets = tickets
        self.result = ExecutionResult()

    def authorize(self, symbol: str, side: str, qty: int) -> tuple[ApprovedTicket | None, str]:
        """Find a ticket that covers this order, or explain why none does."""
        symbol = symbol.upper().strip()
        side = side.lower().strip()
        if qty <= 0:
            return None, "quantity must be positive"
        matches = [t for t in self.tickets if t.symbol == symbol and t.side == side]
        if not matches:
            allowed = ", ".join(f"{t.side} {t.remaining} {t.symbol}" for t in self.tickets) or "nothing"
            return None, (
                f"not authorized: no approved ticket to {side} {symbol}. "
                f"Approved this cycle: {allowed}."
            )
        ticket = max(matches, key=lambda t: t.remaining)
        if ticket.remaining <= 0:
            return None, f"ticket for {side} {symbol} is fully worked ({ticket.qty} shares)"
        if qty > ticket.remaining:
            return None, (
                f"{qty} exceeds the {ticket.remaining} shares still authorized on the "
                f"{side} {symbol} ticket. Resubmit for {ticket.remaining} or fewer."
            )
        return ticket, ""

    def submit(self, ticket: ApprovedTicket, symbol: str, side: str, kind: str,
               qty: int, price: float | None) -> str:
        try:
            resp = self.client.place_order(self.agent_id, symbol, side, kind, qty, price)
        except TradingError as err:
            self.result.denied.append(f"{side} {qty} {symbol} rejected by exchange: {err}")
            return f"exchange rejected the order: {err}"
        ticket.filled += qty
        order = resp.get("order") or {}
        fills = resp.get("fills") or []
        filled = sum(int(f.get("qty", 0)) for f in fills)
        avg = (
            sum(float(f["price"]) * int(f["qty"]) for f in fills) / filled if filled else None
        )
        self.result.orders_sent.append(
            {
                "order_id": order.get("id"),
                "symbol": symbol,
                "side": side,
                "kind": kind,
                "qty": qty,
                "price": price,
                "filled": filled,
                "avg_price": avg,
                "status": order.get("status"),
            }
        )
        if filled:
            return (
                f"order #{order.get('id')} {side} {qty} {symbol}: filled {filled} @ "
                f"{avg:,.2f} avg, status {order.get('status')}. "
                f"{ticket.remaining} shares left on this ticket."
            )
        return (
            f"order #{order.get('id')} {side} {qty} {symbol} resting @ {price}, "
            f"status {order.get('status')}. {ticket.remaining} shares left on this ticket."
        )


def _build_tools(session: _Session) -> list[Any]:
    """Build this cycle's tools, closed over the authorization session."""

    def read_order_book(symbol: str) -> str:
        """Read the current order book for one listing, ten levels a side.

        Use this to check where the touch is before pricing a limit order, and
        to see whether there is enough resting size to absorb your quantity.

        Args:
            symbol: Ticker, e.g. NOVA.
        """
        try:
            book = session.client.book(symbol.upper().strip(), levels=10)
        except TradingError as err:
            return f"could not read the book: {err}"
        bids = book.get("bids") or []
        asks = book.get("asks") or []
        lines = [f"{book.get('symbol')} book — {'bid px':>10}{'sz':>8}   {'ask px':>10}{'sz':>8}"]
        for i in range(max(len(bids), len(asks))):
            b = bids[i] if i < len(bids) else None
            a = asks[i] if i < len(asks) else None
            lines.append(
                f"{'':>16}{(f'{b[0]:,.2f}' if b else '—'):>10}{(f'{b[1]:,.0f}' if b else '—'):>8}   "
                f"{(f'{a[0]:,.2f}' if a else '—'):>10}{(f'{a[1]:,.0f}' if a else '—'):>8}"
            )
        return "\n".join(lines) if (bids or asks) else f"{symbol}: book is empty"

    def place_limit_order(symbol: str, side: str, qty: int, limit_price: float) -> str:
        """Place a limit order. Rejected unless an approved ticket covers it.

        A limit that crosses the book executes at the resting price, so pricing
        through the touch is safe — it never fills worse than your limit.

        Args:
            symbol: Ticker, e.g. NOVA.
            side: "buy" or "sell".
            qty: Shares. Must not exceed the authorized remainder on the ticket.
            limit_price: Price per share.
        """
        ticket, why = session.authorize(symbol, side, qty)
        if ticket is None:
            session.result.denied.append(why)
            return why
        if limit_price <= 0:
            return "limit_price must be positive"
        return session.submit(ticket, symbol.upper().strip(), side.lower().strip(),
                              "limit", qty, round(float(limit_price), 2))

    def place_market_order(symbol: str, side: str, qty: int) -> str:
        """Place a market order. Rejected unless an approved ticket covers it.

        Market orders sweep the book and can slip badly when depth is thin.
        Read the book first; prefer a marketable limit when you can.

        Args:
            symbol: Ticker, e.g. NOVA.
            side: "buy" or "sell".
            qty: Shares. Must not exceed the authorized remainder on the ticket.
        """
        ticket, why = session.authorize(symbol, side, qty)
        if ticket is None:
            session.result.denied.append(why)
            return why
        return session.submit(ticket, symbol.upper().strip(), side.lower().strip(),
                              "market", qty, None)

    def cancel_working_order(order_id: int) -> str:
        """Cancel one of this desk's own resting orders and free its reservation.

        Use this to pull a stale quote before repricing it — resting orders tie
        up cash or shares that a better-priced order could be using.

        Args:
            order_id: The numeric id returned when the order was placed.
        """
        try:
            session.client.cancel_order(int(order_id), session.agent_id)
        except TradingError as err:
            return f"could not cancel #{order_id}: {err}"
        return f"cancelled order #{order_id}; its reservation is released"

    return [
        beta_tool(read_order_book),
        beta_tool(place_limit_order),
        beta_tool(place_market_order),
        beta_tool(cancel_working_order),
    ]


class ExecutionTrader(DeskAgent):
    """Works approved tickets into the book.

    Runs at low effort on purpose: this seat is not forming a view, it is
    getting a decided trade done at a decent price. Depth of reasoning here
    buys latency and tokens, not fills.
    """

    role = "execution_trader"
    effort = "low"

    @property
    def charter(self) -> str:
        return """\
# Your seat: execution trader

You hold the only tools on this desk that reach the exchange. You do not
decide *what* to trade — that argument is over. You decide *how* to get the
approved tickets done at a good price, and then you stop.

## Your authority

You are given a list of approved tickets: symbol, side, and a maximum share
count. That list is the whole of your authority.

* You may fill less than a ticket, slice it across several orders, or skip it
  entirely if the book makes it a bad idea. Say why if you skip.
* You may **not** exceed a ticket, trade a symbol not on the list, or flip a
  side. The tools check every call and will refuse — a refusal is information,
  not a wall to keep pushing against. Read it and adjust.

## Working an order

1. **Read the book first** for any ticket where price matters. The briefing's
   book may be a tick or two stale; `read_order_book` is live.
2. **Passive urgency** → rest inside the spread, one tick better than the
   touch. You are being paid to wait.
3. **Normal urgency** → take the near touch with a marketable limit priced at
   or just through it. You get the resting price, so this cannot fill worse
   than your limit.
4. **Aggressive urgency** → cross with a market order, but check the depth
   first. If your size is more than the near levels can absorb, slice it
   rather than sweeping five levels and paying for all of them.
5. **Cancel before repricing.** A stale resting order is holding cash or
   shares hostage.

## Finishing

Stop when every ticket is worked or you have decided the rest is not worth
doing. Then write two or three sentences: what you sent, what you got, what
you deliberately left undone and why. Do not re-place an order to look busy,
and do not narrate every tool call — the log already has them.
"""

    def run(
        self,
        client: TradingClient,
        agent_id: str,
        tickets: list[ApprovedTicket],
        market_brief: str,
        desk_brief: str,
    ) -> ExecutionResult:
        if not tickets:
            return ExecutionResult(note="no approved tickets this cycle")
        session = _Session(client, agent_id, tickets)
        ticket_list = "\n".join(
            f"- {t.side} up to {t.qty} {t.symbol} "
            f"({t.order_type}, {t.urgency}"
            + (f", PM price {t.limit_price:,.2f}" if t.limit_price else "")
            + f") :: {t.rationale}"
            for t in tickets
        )
        brief = "\n\n".join(
            [
                market_brief,
                desk_brief,
                "## Approved tickets — your entire authority this cycle\n" + ticket_list,
                "Work these now.",
            ]
        )
        session.result.note = self.llm.act(
            role=self.role,
            manual=self.manual,
            charter=self.charter,
            brief=brief,
            tools=_build_tools(session),
            effort=self.effort,
            max_iterations=3 * len(tickets) + 4,
        )
        return session.result
