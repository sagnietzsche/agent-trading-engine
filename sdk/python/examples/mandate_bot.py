"""Cooperative reference bot driven purely by welfare mandates."""

from trading_engine import Agent, MandateStrategy, TradingClient

client = TradingClient("http://127.0.0.1:8080")
agent = Agent.create(client, "mandate-bot")
agent.run(MandateStrategy(), duration_s=120, log=print)
