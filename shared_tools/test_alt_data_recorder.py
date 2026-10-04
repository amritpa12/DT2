import json

import alt_data_recorder as rec


def test_probe_only_exits_zero(capsys):
    assert rec.main(["--probe-only"]) == 0
    assert json.loads(capsys.readouterr().out) == {"status": "ok"}


def test_parse_sources_default_and_unknown():
    assert rec.parse_sources("") == list(rec.DEFAULT_SOURCES)
    assert rec.parse_sources("fng,fng,peg") == ["fng", "peg"]
    try:
        rec.parse_sources("twitter")
    except ValueError as exc:
        assert "twitter" in str(exc)
    else:
        raise AssertionError("unknown source must fail")


def test_collect_fng_keeps_vendor_stamp_without_observed_at():
    def fetch(url, body=None):
        assert "alternative.me" in url
        return {"data": [{"value": "24", "value_classification": "Fear", "timestamp": "1759536000"}]}

    rows = rec.collect(["fng"], fetch=fetch)
    assert len(rows) == 1
    assert rows[0]["source_id"] == "fng"
    assert rows[0]["asset"] == "BTC"
    assert rows[0]["published_at"].startswith("2025-10-04")
    assert "observed_at" not in rows[0]
    assert rows[0]["raw_json"]["value"] == "24"


def test_collect_hl_meta_records_flagged_names_only():
    def fetch(url, body=None):
        assert body == {"type": "meta"}
        return {"universe": [
            {"name": "BTC", "isDelisted": False},
            {"name": "ABC", "isDelisted": True},
            {"name": "DEF", "isHalt": True},
        ]}

    rows = rec.collect(["hl_meta"], fetch=fetch)
    flagged = {item["name"] for item in rows[0]["raw_json"]["flagged"]}
    assert flagged == {"ABC", "DEF"}
    assert rows[0]["raw_json"]["universe_len"] == 3


def test_collect_peg_requires_two_venues():
    def fetch(url, body=None):
        if body == {"type": "allMids"}:
            return {"USDT": "1.002", "BTC": "100000"}
        if "USDC-USD" in url:
            return {"data": {"amount": "0.999"}}
        if "USDT-USD" in url:
            return {"data": {"amount": "1.001"}}
        raise AssertionError(url)

    rows = rec.collect(["peg"], fetch=fetch)
    assets = {row["asset"] for row in rows}
    assert assets == {"USDC", "USDT"}
    venues = rows[0]["raw_json"]["venues"]
    assert "hyperliquid" in venues and "coinbase" in venues
    assert venues["coinbase"]["USDC"] == 0.999
