#!/usr/bin/env python3
"""T0 event-risk feed (HL meta delist/halt, two-venue stablecoin peg).

Prints JSON events on stdout. The Go poller overwrites observed_at with its
own receipt clock — this script must never treat a vendor timestamp as the
as-of time. T2 sources never emit events here.
"""

from __future__ import annotations

import argparse
import hashlib
import json
import sys
import urllib.request
from typing import Any, Callable, Iterable

DEFAULT_SOURCES = ("hl_meta", "peg")
ALLOWED_SOURCES = frozenset(DEFAULT_SOURCES)

HL_INFO_URL = "https://api.hyperliquid.xyz/info"
COINBASE_USDC_URL = "https://api.coinbase.com/v2/prices/USDC-USD/spot"
COINBASE_USDT_URL = "https://api.coinbase.com/v2/prices/USDT-USD/spot"
DEPEG_ABS = 0.01

FetchFn = Callable[[str, dict[str, Any] | None], Any]


def url_hash(url: str) -> str:
    return hashlib.sha256(url.encode("utf-8")).hexdigest()[:16]


def default_fetch(url: str, body: dict[str, Any] | None = None) -> Any:
    data = None
    headers = {"User-Agent": "go-trader-event-risk-feed/1"}
    if body is not None:
        data = json.dumps(body).encode("utf-8")
        headers["Content-Type"] = "application/json"
    req = urllib.request.Request(url, data=data, headers=headers)
    with urllib.request.urlopen(req, timeout=20) as resp:
        raw = resp.read()
    return json.loads(raw.decode("utf-8"))


def event(
    source_id: str,
    *,
    tier: str,
    asset: str,
    event_type: str,
    venue_or_protocol: str,
    title: str,
    url: str = "",
    payload: Any = None,
) -> dict[str, Any]:
    return {
        "source_id": source_id,
        "tier": tier,
        "asset": asset,
        "event_type": event_type,
        "venue_or_protocol": venue_or_protocol,
        "title": title,
        "url_hash": url_hash(url) if url else "",
        "raw_json": payload if payload is not None else {},
    }


def fetch_hl_meta(fetch: FetchFn) -> list[dict[str, Any]]:
    payload = fetch(HL_INFO_URL, {"type": "meta"})
    universe = payload.get("universe") if isinstance(payload, dict) else None
    if not isinstance(universe, list):
        raise ValueError("hl_meta: missing universe[]")
    out: list[dict[str, Any]] = []
    for item in universe:
        if not isinstance(item, dict):
            continue
        name = str(item.get("name") or "").strip().upper()
        if not name:
            continue
        if item.get("isDelisted"):
            out.append(event(
                "hl_meta",
                tier="t0",
                asset=name,
                event_type="delist",
                venue_or_protocol="hyperliquid",
                title=f"Hyperliquid delist {name}",
                url=HL_INFO_URL,
                payload=item,
            ))
        if item.get("isHalt"):
            out.append(event(
                "hl_meta",
                tier="t0",
                asset=name,
                event_type="halt",
                venue_or_protocol="hyperliquid",
                title=f"Hyperliquid halt {name}",
                url=HL_INFO_URL,
                payload=item,
            ))
    return out


def _coinbase_spot(payload: Any) -> float:
    if not isinstance(payload, dict):
        raise ValueError("coinbase spot: not an object")
    amount = (payload.get("data") or {}).get("amount")
    return float(amount)


def _mid(value: Any) -> float | None:
    if value in (None, ""):
        return None
    try:
        return float(value)
    except (TypeError, ValueError):
        return None


def fetch_peg(fetch: FetchFn) -> list[dict[str, Any]]:
    mids = fetch(HL_INFO_URL, {"type": "allMids"})
    if not isinstance(mids, dict):
        raise ValueError("peg: allMids is not an object")
    usdc = _coinbase_spot(fetch(COINBASE_USDC_URL, None))
    usdt = _coinbase_spot(fetch(COINBASE_USDT_URL, None))
    hl_usdc = _mid(mids.get("USDC"))
    hl_usdt = _mid(mids.get("USDT"))
    out: list[dict[str, Any]] = []
    for asset, hl_px, cb_px in (
        ("USDC", hl_usdc, usdc),
        ("USDT", hl_usdt, usdt),
    ):
        if hl_px is None:
            continue
        if abs(hl_px - 1.0) >= DEPEG_ABS and abs(cb_px - 1.0) >= DEPEG_ABS:
            out.append(event(
                "peg",
                tier="t0",
                asset=asset,
                event_type="depeg",
                venue_or_protocol="multi_venue",
                title=f"{asset} depeg HL={hl_px} Coinbase={cb_px}",
                url=HL_INFO_URL,
                payload={
                    "venues": {
                        "hyperliquid": {asset: hl_px},
                        "coinbase": {asset: cb_px},
                    }
                },
            ))
    return out


FETCHERS = {
    "hl_meta": fetch_hl_meta,
    "peg": fetch_peg,
}


def collect(sources: Iterable[str], fetch: FetchFn = default_fetch) -> list[dict[str, Any]]:
    events: list[dict[str, Any]] = []
    for src in sources:
        if src not in FETCHERS:
            raise ValueError(f"unknown source {src!r}")
        events.extend(FETCHERS[src](fetch))
    return events


def parse_sources(raw: str) -> list[str]:
    if not raw.strip():
        return list(DEFAULT_SOURCES)
    out: list[str] = []
    seen: set[str] = set()
    for part in raw.split(","):
        src = part.strip().lower()
        if not src or src in seen:
            continue
        if src not in ALLOWED_SOURCES:
            raise ValueError(f"unknown source {src!r} (want {', '.join(sorted(ALLOWED_SOURCES))})")
        seen.add(src)
        out.append(src)
    return out or list(DEFAULT_SOURCES)


def main(argv: list[str] | None = None) -> int:
    parser = argparse.ArgumentParser(description="Emit T0 venue/peg events")
    parser.add_argument("--probe-only", action="store_true")
    parser.add_argument("--sources", default=",".join(DEFAULT_SOURCES))
    args = parser.parse_args(argv)
    if args.probe_only:
        print(json.dumps({"status": "ok"}))
        return 0
    try:
        sources = parse_sources(args.sources)
        events = collect(sources)
    except Exception as exc:
        print(f"event_risk_feed: {exc}", file=sys.stderr)
        return 1
    print(json.dumps({"events": events}, separators=(",", ":")))
    return 0


if __name__ == "__main__":
    sys.exit(main())
