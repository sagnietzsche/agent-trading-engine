"""Turning a live frame into something an agent can actually reason about.

A raw ``/api/ws`` frame is a few kilobytes of JSON with ids, timestamps, and
fields no trading decision depends on. Handing that to a model wastes tokens
and buries the signal. :func:`market_brief` renders the same state as a short
markdown briefing: the numbers a trader would actually look at, pre-computed
where a model would otherwise have to do arithmetic in its head.

Two rules shape everything here:

* **No wall clock.** Timestamps in a prompt are the classic silent cache
  invalidator. Frames are identified by sequence number instead.
* **Derived, not raw.** Spread in basis points, move versus previous close,
  and imbalance are computed here so the model spends its thinking on the
  decision rather than on division.
"""

from __future__ import annotations

from typing import Any, Mapping, Sequence

__all__ = ["desk_brief", "market_brief", "marks_from_frame", "venue_manual"]


def _f(v: Any) -> float | None:
    return None if v is None else float(v)


def _fmt(v: float | None, nd: int = 2) -> str:
    return "—" if v is None else f"{v:,.{nd}f}"


def marks_from_frame(frame: Mapping[str, Any]) -> dict[str, float]:
    """Current mark per symbol: last trade if there is one, else fair value."""
    marks: dict[str, float] = {}
    for stock in frame.get("stocks") or []:
        last = _f(stock.get("last_trade"))
        marks[stock["symbol"]] = last if last else float(stock.get("fair") or 0.0)
    return marks


def _stock_table(stocks: Sequence[Mapping[str, Any]]) -> str:
    head = f"{'sym':<6}{'last':>10}{'bid':>10}{'ask':>10}{'spr(bp)':>9}{'vs close':>10}"
    rows = [head, "-" * len(head)]
    for s in stocks:
        last, bid, ask = _f(s.get("last_trade")), _f(s.get("bid")), _f(s.get("ask"))
        prev = float(s.get("prev_close") or 0.0)
        mid = (bid + ask) / 2 if bid and ask else last
        spread_bp = (ask - bid) / mid * 10_000 if bid and ask and mid else None
        chg = (last / prev - 1.0) * 100 if last and prev else None
        rows.append(
            f"{s['symbol']:<6}{_fmt(last):>10}{_fmt(bid):>10}{_fmt(ask):>10}"
            f"{_fmt(spread_bp, 1):>9}{('—' if chg is None else f'{chg:+.2f}%'):>10}"
        )
    return "\n".join(rows)


def _book_section(book: Mapping[str, Any] | None) -> str:
    if not book:
        return "(no book)"
    bids = book.get("bids") or []
    asks = book.get("asks") or []
    bid_size = sum(level[1] for level in bids)
    ask_size = sum(level[1] for level in asks)
    total = bid_size + ask_size
    imbalance = (bid_size - ask_size) / total if total else 0.0
    lines = [
        f"{book.get('symbol', '?')} — depth imbalance {imbalance:+.2f} "
        f"(bid {bid_size:,.0f} vs ask {ask_size:,.0f} shares)",
        f"{'bid px':>10}{'bid sz':>9}   {'ask px':>10}{'ask sz':>9}",
    ]
    for i in range(min(6, max(len(bids), len(asks)))):
        b = bids[i] if i < len(bids) else None
        a = asks[i] if i < len(asks) else None
        lines.append(
            f"{_fmt(b[0]) if b else '—':>10}{(f'{b[1]:,.0f}' if b else '—'):>9}   "
            f"{_fmt(a[0]) if a else '—':>10}{(f'{a[1]:,.0f}' if a else '—'):>9}"
        )
    return "\n".join(lines)


def _tape_section(tape: Sequence[Mapping[str, Any]], limit: int = 12) -> str:
    if not tape:
        return "(no prints yet — nothing has crossed the spread)"
    rows = [f"{'sym':<6}{'px':>10}{'qty':>8}"]
    for t in list(tape)[:limit]:
        rows.append(f"{t['symbol']:<6}{float(t['price']):>10,.2f}{int(t['qty']):>8,}")
    return "\n".join(rows)


def _chat_section(chat: Sequence[Mapping[str, Any]], limit: int = 8) -> str:
    if not chat:
        return "(floor is quiet)"
    return "\n".join(
        f"{m.get('name', '?')}: {m.get('text', '')}" for m in list(chat)[:limit]
    )


def venue_manual(regime: str = "neutral") -> str:
    """The shared, stable system prompt every agent on the desk receives.

    This is deliberately long and deliberately fixed: it is the cached prefix
    for every call the desk makes, so its bytes must not vary between agents
    or between cycles. Anything that changes tick to tick belongs in the
    briefing, not here.
    """
    solidarity = regime == "solidarity"
    regime_para = (
        "This instance runs the SOLIDARITY regime. On top of ordinary matching, "
        "the venue issues per-agent mandates that suggest wealth-equalising "
        "trades, and orders flagged as solidarity orders are routed to the "
        "worst-off counterparties before best price. Mandates are advisory: you "
        "are not obliged to follow them, but they tell you where the venue is "
        "about to push flow, which is tradeable information."
        if solidarity
        else "This instance runs the NEUTRAL regime — a conventional exchange. "
        "Strict price-time priority, no mandates, no redistribution. Nobody is "
        "steering the outcome; every decision on this desk is yours."
    )
    return f"""\
# Venue manual — agent-trading-engine

You are one role on an autonomous trading desk operating on a simulated
exchange. No real money and no real market data are involved; the listings are
fictional. Your job is to trade this venue well.

## The exchange

Six listings trade continuously: NOVA (Nova Dynamics), QNTM (Quantum Foundry),
HELX (Helix Biolabs), DRCT (Direct Commons), ORBT (Orbital Logistics), ZEPH
(Zephyr Energy).

* **Matching** is a central limit order book with price-time priority. A
  limit order that crosses executes at the resting (maker) price, so crossing
  is never worse than your limit.
* **Order types** are `limit` (a price you name) and `market` (whatever the
  book gives you). Market orders on a thin book slip badly.
* **Settlement is instant** and there is **no leverage and no borrow**: a buy
  needs free cash, a sell needs free shares. Shorting is impossible — a sell
  you cannot deliver is simply rejected.
* **Reservations**: a resting buy reserves cash, a resting sell reserves
  shares. Free cash and free shares are what remains after reservations, and
  they are the only balances that can back a new order.
* **Self-trading is blocked.** Your own resting orders are invisible to your
  own aggressive orders.
* **Two system agents** always quote: a market maker keeps a tight two-sided
  book near fair value, and a second agent rests larger size further out. They
  are patient and they do not chase — they are liquidity, not competition.
* **Fair value** random-walks each tick. The market maker anchors to it, so
  the mid reverts toward fair; a price far from fair is usually an imbalance
  rather than news.

## Regime

{regime_para}

## Welfare read-out

The venue publishes an inequality index over agent equity (Gini by default;
Atkinson or Nash on some instances). Under the neutral regime it is pure
observability — a statistic about the population, with no effect on matching.

## How this desk works

Five agents run in sequence each cycle, each with one job and no authority
over anyone else's:

1. **Market analyst** — reads price, book, and tape. Produces signals. Cannot trade.
2. **Event strategist** — reads scenario definitions and the floor chat.
   Produces a narrative and an exposure recommendation. Cannot trade.
3. **Portfolio manager** — turns those reads into concrete orders. Cannot trade.
4. **Risk officer** — rules on the PM's orders. May shrink or veto, never enlarge.
5. **Execution trader** — the only role holding tools that reach the exchange,
   and it may only work tickets that already cleared risk.

After the risk officer rules, a deterministic limit checker clamps every
ticket again in code. Being approved by the risk officer does not guarantee an
order is sent.

## Standing instructions

* Trade only when you can name the evidence. "No trade" is a complete answer
  and costs nothing; a trade you cannot justify costs the spread.
* Prefer limit orders inside the spread. Reach for market orders only when
  being filled matters more than the price.
* Size to survive being wrong. The desk's edge is small and repeated.
* Never invent a number. If the briefing does not contain it, say so.
"""


def market_brief(frame: Mapping[str, Any], cycle: int) -> str:
    """Render the shared market picture handed to every analysis agent."""
    welfare = frame.get("welfare") or {}
    stocks = frame.get("stocks") or []
    parts = [
        f"# Market briefing — cycle {cycle}, frame seq {frame.get('seq', '?')}",
        "",
        "## Listings",
        _stock_table(stocks),
        "",
        f"## Order book — {(frame.get('book') or {}).get('symbol', 'n/a')}",
        _book_section(frame.get("book")),
        "",
        "## Recent prints (newest first)",
        _tape_section(frame.get("tape") or []),
        "",
        "## Venue statistics",
        f"regime={welfare.get('regime', 'neutral')} "
        f"metric={welfare.get('metric', 'gini')} "
        f"inequality={float(welfare.get('gini') or 0.0):.3f} "
        f"total_equity=${float(welfare.get('total_equity') or 0.0):,.0f}",
        "",
        "## Floor chat (newest first)",
        _chat_section(frame.get("chat") or []),
    ]
    return "\n".join(parts)


def desk_brief(desk: Mapping[str, Any], marks: Mapping[str, float]) -> str:
    """Render our own book: cash, positions, and working orders."""
    positions = desk.get("positions") or []
    equity = float(desk.get("equity") or 0.0)
    lines = [
        "## Our desk",
        f"equity=${equity:,.2f} cash=${float(desk.get('cash') or 0.0):,.2f} "
        f"free_cash=${float(desk.get('free_cash') or 0.0):,.2f} "
        f"reserved=${float(desk.get('reserved_cash') or 0.0):,.2f}",
    ]
    if not positions:
        lines.append("positions: flat")
    else:
        head = f"{'sym':<6}{'qty':>8}{'free':>8}{'mark':>10}{'value':>12}{'% eq':>7}"
        lines += ["", head, "-" * len(head)]
        for p in positions:
            value = float(p.get("value") or 0.0)
            pct = value / equity * 100 if equity else 0.0
            lines.append(
                f"{p['symbol']:<6}{int(p['qty']):>8,}{int(p['free']):>8,}"
                f"{float(p['mark']):>10,.2f}{value:>12,.0f}{pct:>6.1f}%"
            )
    orders = desk.get("open_orders") or []
    if orders:
        lines += ["", "working orders:"]
        for o in orders[:10]:
            px = _fmt(_f(o.get("price")))
            lines.append(
                f"  #{o['id']} {o['side']} {int(o['qty']) - int(o.get('filled', 0))}"
                f"/{int(o['qty'])} {o['symbol']} @ {px} ({o.get('status')})"
            )
    else:
        lines.append("working orders: none")
    return "\n".join(lines)
