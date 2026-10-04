import json
import pathlib
import sys

import pytest

sys.path.insert(0, str(pathlib.Path(__file__).parent.parent.parent / "shared_tools"))
sys.path.insert(0, str(pathlib.Path(__file__).parent.parent))

import run_backtest


def _config(tmp_path, strategy):
    cfg = {"config_version": 16, "strategies": [strategy]}
    path = tmp_path / "config.json"
    path.write_text(json.dumps(cfg))
    return str(path)


def _hl_strategy(**over):
    sc = {
        "id": "hl-test", "type": "perps", "platform": "hyperliquid",
        "script": "shared_scripts/check_hyperliquid.py",
        "args": ["tema_cross", "ETH", "4h", "--mode", "paper"],
        "capital": 1000, "max_drawdown_pct": 50,
        "open_strategy": {"name": "tema_cross", "params": {}},
        "stop_loss_atr_mult": 2.0,
    }
    sc.update(over)
    return sc


def test_rejects_enabled_event_risk_gate(tmp_path):
    path = _config(tmp_path, _hl_strategy(
        event_risk_gate={"enabled": True, "on_failure": "open"},
    ))
    with pytest.raises(ValueError, match="event_risk_gate"):
        run_backtest.load_strategy_config(path, "hl-test")


@pytest.mark.parametrize("gate", [
    {"enabled": False},
    None,
])
def test_allows_config_without_enabled_event_risk_gate(tmp_path, gate):
    over = {} if gate is None else {"event_risk_gate": gate}
    path = _config(tmp_path, _hl_strategy(**over))
    kwargs = run_backtest.load_strategy_config(path, "hl-test")
    assert kwargs["open_strategy"] == {"name": "tema_cross", "params": {}}
