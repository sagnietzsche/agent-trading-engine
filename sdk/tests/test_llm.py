"""Model policy, cost accounting, and credential detection.

None of this calls Claude — it is the bookkeeping around the calls, which is
the part that silently goes wrong on a long-running multi-agent pipeline.
"""

import anthropic
import pytest

from trading_engine.llm import DeskLLM, Ledger, ModelPolicy, RoleUsage, credentials_available


class FakeUsage:
    def __init__(self, inp=0, out=0, write=0, read=0):
        self.input_tokens = inp
        self.output_tokens = out
        self.cache_creation_input_tokens = write
        self.cache_read_input_tokens = read


def test_ledger_meters_each_role_separately():
    led = Ledger()
    led.record("market_analyst", FakeUsage(1000, 500), 2.0)
    led.record("market_analyst", FakeUsage(1000, 500), 2.0)
    led.record("risk_officer", FakeUsage(200, 100), 1.0)

    assert led.total_calls == 3
    assert led.roles["market_analyst"].input_tokens == 2000
    assert led.roles["market_analyst"].seconds == 4.0
    assert led.roles["risk_officer"].calls == 1


def test_cache_hit_rate_reflects_what_was_actually_served_from_cache():
    led = Ledger()
    led.record("a", FakeUsage(inp=100, write=0, read=900), 0.1)
    assert led.cache_hit_rate == pytest.approx(0.9)

    cold = Ledger()
    cold.record("a", FakeUsage(inp=1000), 0.1)
    assert cold.cache_hit_rate == 0.0

    assert Ledger().cache_hit_rate == 0.0  # no calls, no division by zero


def test_cost_is_priced_against_the_model_actually_in_use():
    usage = RoleUsage(input_tokens=1_000_000, output_tokens=1_000_000)
    opus = usage.cost("claude-opus-5")
    haiku = usage.cost("claude-haiku-4-5")
    assert opus == pytest.approx(30.0)   # $5 in + $25 out
    assert haiku == pytest.approx(6.0)   # $1 in + $5 out
    # An unknown id falls back rather than pricing at zero.
    assert usage.cost("something-new") == opus


def test_ledger_total_uses_its_configured_model():
    led = Ledger(model="claude-haiku-4-5")
    led.record("a", FakeUsage(inp=1_000_000, out=1_000_000), 1.0)
    assert led.total_cost_usd == pytest.approx(6.0)


def test_a_refusal_is_counted_without_losing_its_token_cost():
    led = Ledger()
    led.record("pm", FakeUsage(500, 10), 1.0, refused=True)
    assert led.roles["pm"].refusals == 1
    assert led.roles["pm"].input_tokens == 500


def test_ledger_table_renders_every_role_and_a_total():
    led = Ledger()
    led.record("market_analyst", FakeUsage(100, 50, read=900), 1.5)
    led.record("risk_officer", FakeUsage(80, 20), 0.5)
    table = led.table()
    assert "market_analyst" in table and "risk_officer" in table
    assert table.strip().splitlines()[-1].startswith("total")


def test_the_cached_prefix_comes_first_and_the_charter_does_not_carry_a_breakpoint():
    blocks = DeskLLM.system_blocks("MANUAL", "CHARTER")
    assert blocks[0]["text"] == "MANUAL"
    assert blocks[0]["cache_control"] == {"type": "ephemeral"}
    assert blocks[1]["text"] == "CHARTER"
    assert "cache_control" not in blocks[1]


def test_effort_is_a_per_role_override_of_the_desk_policy():
    policy = ModelPolicy(effort="high")
    assert policy.with_effort("low").effort == "low"
    assert policy.effort == "high"          # the base policy is untouched
    assert policy.with_effort("low").model == policy.model


def test_credentials_are_detected_from_the_constructed_client_not_the_environment():
    """The constructor succeeds with nothing configured, so ask what it resolved."""
    assert credentials_available(anthropic.Anthropic(api_key="sk-test"))

    class Unconfigured:
        api_key = None
        auth_token = None
        credentials = None

    assert not credentials_available(Unconfigured())
