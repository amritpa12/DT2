import json

import event_risk_feed as feed


def test_probe_only_exits_zero(capsys):
    assert feed.main(["--probe-only"]) == 0
    assert json.loads(capsys.readouterr().out) == {"status": "ok"}


def test_parse_sources_default_and_unknown():
    assert feed.parse_sources("") == list(feed.DEFAULT_SOURCES)
    assert feed.parse_sources("hl_meta,hl_meta,peg") == ["hl_meta", "peg"]
    try:
        feed.parse_sources("twitter")
    except ValueError as exc:
        assert "twitter" in str(exc)
    else:
        raise AssertionError("unknown source must fail")


def test_hl_meta_emits_t0_delist_and_halt_without_observed_at():
    def fetch(url, body=None):
        assert body == {"type": "meta"}
        return {"universe": [
            {"name": "BTC", "isDelisted": False},
            {"name": "ABC", "isDelisted": True},
            {"name": "DEF", "isHalt": True},
        ]}

    rows = feed.collect(["hl_meta"], fetch=fetch)
    kinds = {(row["asset"], row["event_type"], row["tier"]) for row in rows}
    assert kinds == {("ABC", "delist", "t0"), ("DEF", "halt", "t0")}
    assert all("observed_at" not in row for row in rows)


def test_peg_requires_two_venues_beyond_threshold():
    def fetch(url, body=None):
        if body == {"type": "allMids"}:
            return {"USDT": "0.97", "USDC": "1.0", "BTC": "100000"}
        if "USDC-USD" in url:
            return {"data": {"amount": "1.0"}}
        if "USDT-USD" in url:
            return {"data": {"amount": "0.98"}}
        raise AssertionError(url)

    rows = feed.collect(["peg"], fetch=fetch)
    assert len(rows) == 1
    assert rows[0]["asset"] == "USDT"
    assert rows[0]["event_type"] == "depeg"
    assert rows[0]["tier"] == "t0"
    assert "observed_at" not in rows[0]


def test_peg_one_venue_alone_does_not_emit():
    def fetch(url, body=None):
        if body == {"type": "allMids"}:
            return {"USDT": "0.90"}
        if "USDC-USD" in url:
            return {"data": {"amount": "1.0"}}
        if "USDT-USD" in url:
            return {"data": {"amount": "1.0"}}
        raise AssertionError(url)

    assert feed.collect(["peg"], fetch=fetch) == []
