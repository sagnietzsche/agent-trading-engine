"""Run one greedy and one cooperative bot side by side."""

import threading

from trading_engine import (
    Agent,
    GreedyMomentumStrategy,
    MandateStrategy,
    TradingClient,
)

client = TradingClient("http://127.0.0.1:8080")

cooperative = Agent.create(client, "coop-bot")
greedy = Agent.create(client, "greed-bot")

t = threading.Thread(
    target=lambda: greedy.run(GreedyMomentumStrategy(), duration_s=90, use_ws=False),
    daemon=True,
)
t.start()

cooperative.run(MandateStrategy(), duration_s=90, log=print)
t.join(timeout=5)

print("coop equity :", client.agent(cooperative.agent_id)["equity"])
print("greed equity:", client.agent(greedy.agent_id)["equity"])
