"""Replace one seat on the desk without touching the rest of it.

    python sdk/examples/custom_seat.py

Every agent is a charter plus an output schema, so swapping a seat means
subclassing it and overriding ``charter``. Here the risk officer is replaced
with a far more conservative one. Nothing else in the pipeline changes — the
schema at the seam is what keeps the swap safe.
"""

from trading_engine import DeskConfig, RiskLimits, TradingClient, TradingDesk
from trading_engine.agents import RiskOfficer


class DrawdownHawk(RiskOfficer):
    """A risk officer that treats capital preservation as the only mandate."""

    role = "risk_officer"
    effort = "medium"

    @property
    def charter(self) -> str:
        # Extending the stock charter rather than replacing it keeps the
        # output-contract rules (one verdict per trade, never enlarge) intact.
        return super().charter + """

## Additional standing instruction — conservative mandate

This desk is in capital-preservation mode. On top of everything above:

* Halve any ticket whose rationale rests on a conviction below 0.6.
* Reject any buy that would take gross exposure above 40% of equity, whatever
  the strategist recommended.
* Reject any market order. If being filled is worth crossing, it is worth a
  marketable limit instead.
* When in doubt, reduce. A missed trade costs nothing here.
"""


def main() -> None:
    client = TradingClient("http://127.0.0.1:8080")
    desk = TradingDesk(
        client,
        DeskConfig(name="hawk", cycles=3, limits=RiskLimits(max_gross_exposure=0.40)),
    )
    # The desk builds a stock set of seats; replace the one we care about.
    desk.risk = DrawdownHawk(desk.llm, desk.manual)
    desk.run()


if __name__ == "__main__":
    main()
