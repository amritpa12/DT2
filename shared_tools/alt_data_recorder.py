#!/usr/bin/env python3
"""Point-in-time alt-data recorder.

Fetches public vendor payloads and prints JSON on stdout. The Go poller
overwrites observed_at with its own receipt clock — this script must never
treat a vendor timestamp as the as-of time.
"""

from __future__ import annotations

import argparse
import hashlib
import json
import sys
import urllib.error
import urllib.request
from datetime import datetime, timezone
from typing import Any, Callable, Iterable

DEFAULT_SOURCES = ("fng", "hl_meta", "peg")
ALLOWED_SOURCES = frozenset(DEFAULT_SOURCES)

FNG_URL = "https://api.alternative.me/fng/?limit=1"
HL_INFO_URL = "https://api.hyperliquid.xyz/info"
COINBASE_USDC_URL = "https://api.coinbase.com/v2/prices/USDC-USD/spot"
COINBASE_USDT_URL = "https://api.coinbase.com/v2/prices/USDT-USD/spot"

FetchFn = Callable[[str, dict[str, Any] | None], Any]


def utc_now() -> str:
    return datetime.now(timezone.utc).isoformat()


def url_hash(url: str) -> str:
    return hashlib.sha256(url.encode("utf-8")).hexdigest()[:16]


def default_fetch(url: str, body: dict[str, Any] | None = None) -> Any:
    data = None
    headers = {"User-Agent": "go-trader-alt-data-recorder/1"}
    if body is not None:
        data = json.dumps(body).encode("utf-8")
        headers["Content-Type"] = "application/json"
    req = urllib.request.Request(url, data=data, headers=headers)
    with urllib.request.urlopen(req, timeout=20) as resp:
        raw = resp.read()
    return json.loads(raw.decode("utf-8"))


def reading(
    source_id: str,
    *,
    asset: str = "",
    published_at: str = "",
    url: str = "",
    payload: Any,
) -> dict[str, Any]:
    return {
        "source_id": source_id,
        "asset": asset,
        "published_at": published_at,
        "url_hash": url_hash(url) if url else "",
        "raw_json": payload,
    }


def fetch_fng(fetch: FetchFn) -> list[dict[str, Any]]:
    payload = fetch(FNG_URL, None)
    rows = payload.get("data") if isinstance(payload, dict) else None
    if not isinstance(rows, list) or not rows:
        raise ValueError("fng: missing data[]")
    row = rows[0]
    published = ""
    ts = row.get("timestamp") if isinstance(row, dict) else None
    if ts not in (None, ""):
        try:
            published = datetime.fromtimestamp(int(ts), tz=timezone.utc).isoformat()
        except (TypeError, ValueError):
            published = str(ts)
    return [reading("fng", asset="BTC", published_at=published, url=FNG_URL, payload=row)]


def fetch_hl_meta(fetch: FetchFn) -> list[dict[str, Any]]:
    payload = fetch(HL_INFO_URL, {"type": "meta"})
    universe = payload.get("universe") if isinstance(payload, dict) else None
    if not isinstance(universe, list):
        raise ValueError("hl_meta: missing universe[]")
    flagged = []
    for item in universe:
        if not isinstance(item, dict):
            continue
        name = str(item.get("name") or "").strip().upper()
        if not name:
            continue
        if item.get("isDelisted") or item.get("isHalt") or item.get("onlyIsolated"):
            flagged.append({"name": name, "isDelisted": bool(item.get("isDelisted")),
                            "isHalt": bool(item.get("isHalt")),
                            "onlyIsolated": bool(item.get("onlyIsolated"))})
    return [reading(
        "hl_meta",
        published_at="",
        url=HL_INFO_URL,
        payload={"flagged": flagged, "universe_len": len(universe)},
    )]


def _coinbase_spot(payload: Any) -> float:
    if not isinstance(payload, dict):
        raise ValueError("coinbase spot: not an object")
    amount = (payload.get("data") or {}).get("amount")
    return float(amount)


def fetch_peg(fetch: FetchFn) -> list[dict[str, Any]]:
    mids = fetch(HL_INFO_URL, {"type": "allMids"})
    if not isinstance(mids, dict):
        raise ValueError("peg: allMids is not an object")
    usdc = fetch(COINBASE_USDC_URL, None)
    usdt = fetch(COINBASE_USDT_URL, None)
    payload = {
        "venues": {
            "hyperliquid": {k: mids.get(k) for k in ("USDT", "USDC") if k in mids},
            "coinbase": {
                "USDC": _coinbase_spot(usdc),
                "USDT": _coinbase_spot(usdt),
            },
        }
    }
    out = [reading("peg", asset="USDC", url=COINBASE_USDC_URL, payload=payload)]
    out.append(reading("peg", asset="USDT", url=COINBASE_USDT_URL, payload=payload))
    return out


FETCHERS = {
    "fng": fetch_fng,
    "hl_meta": fetch_hl_meta,
    "peg": fetch_peg,
}


def collect(sources: Iterable[str], fetch: FetchFn = default_fetch) -> list[dict[str, Any]]:
    readings: list[dict[str, Any]] = []
    for src in sources:
        if src not in FETCHERS:
            raise ValueError(f"unknown source {src!r}")
        readings.extend(FETCHERS[src](fetch))
    return readings


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
    parser = argparse.ArgumentParser(description="Archive public alt-data at receipt time")
    parser.add_argument("--probe-only", action="store_true")
    parser.add_argument("--sources", default=",".join(DEFAULT_SOURCES))
    args = parser.parse_args(argv)
    if args.probe_only:
        print(json.dumps({"status": "ok"}))
        return 0
    try:
        sources = parse_sources(args.sources)
        readings = collect(sources)
    except Exception as exc:
        print(f"alt_data_recorder: {exc}", file=sys.stderr)
        return 1
    print(json.dumps({"readings": readings}, separators=(",", ":")))
    return 0


if __name__ == "__main__":
    sys.exit(main())
