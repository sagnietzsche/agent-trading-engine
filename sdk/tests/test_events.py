import json
from pathlib import Path

import pytest

from trading_engine import Event, EventError, load_event, load_events
from trading_engine.events import extract_json_block

HERE = Path(__file__).resolve().parent
EVENTS = HERE.parent / "events"


def test_all_bundled_events_load_and_validate():
    files = sorted(EVENTS.glob("*.md"))
    assert len(files) >= 3
    for p in files:
        ev = load_event(p)
        assert ev.id, p.name
        assert 1 <= ev.severity <= 10, p.name
        assert ev.duration_ticks > 0, p.name
        assert ev.volatility >= 1.0, p.name
        assert ev.spread_multiplier >= 1.0, p.name
        assert ev.headline(), p.name


def test_load_events_directory():
    events = load_events(EVENTS)
    ids = {e.id for e in events}
    # README.md is the format spec, not an event — it must be skipped.
    assert ids == {"global_recession", "war_like_situation", "armageddon"}, ids


def test_bundled_events_are_distinct_scenarios():
    events = {e.id: e for e in load_events(EVENTS)}
    armageddon = events["armageddon"]
    recession = events["global_recession"]
    # Armageddon must be strictly worse than a plain recession.
    assert armageddon.severity > recession.severity
    assert armageddon.volatility > recession.volatility
    assert min(armageddon.shock["symbols"].values()) < min(recession.shock["symbols"].values())


def test_roundtrip():
    ev = load_event(EVENTS / "GLOBAL_RECESSION.md")
    assert Event.from_dict(ev.to_dict()) == ev


def test_extract_json_block():
    text = "intro\n```json\n{\"a\": 1}\n```\nmore"
    assert json.loads(extract_json_block(text)) == {"a": 1}


def test_extract_json_block_rejects_plain_text():
    with pytest.raises(EventError):
        extract_json_block("no code fences here")


def valid_raw() -> dict:
    return {
        "id": "flash_crash",
        "name": "Flash crash",
        "severity": 5,
        "kind": "systemic",
        "duration_ticks": 60,
        "shock": {"symbols": {"NOVA": -0.20}, "ticks": 5, "decay": 0.1},
        "volatility": 3.0,
        "spread_multiplier": 2.0,
    }


def test_missing_required_field_rejected():
    raw = valid_raw()
    del raw["name"]
    with pytest.raises(EventError, match="name"):
        Event.from_dict(raw)


def test_severity_out_of_range():
    raw = valid_raw()
    raw["severity"] = 11
    with pytest.raises(EventError, match="severity"):
        Event.from_dict(raw)


def test_shock_move_out_of_bounds():
    raw = valid_raw()
    raw["shock"]["symbols"]["NOVA"] = -1.5
    with pytest.raises(EventError, match="shock.symbols"):
        Event.from_dict(raw)


def test_unknown_field_rejected():
    raw = valid_raw()
    raw["volatlity"] = 9  # typo — must not be silently ignored
    with pytest.raises(EventError, match="volatlity"):
        Event.from_dict(raw)


def test_bad_float_type_rejected():
    raw = valid_raw()
    raw["volatility"] = "huge"
    with pytest.raises(EventError, match="volatility"):
        Event.from_dict(raw)


def test_missing_file_raises():
    with pytest.raises(EventError, match="not found"):
        load_event(EVENTS / "DOES_NOT_EXIST.md")
