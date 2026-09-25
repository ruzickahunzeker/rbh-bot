#!/usr/bin/env python3
"""Read-only multi-sample C04 RPC tag probe. Never signs or sends transactions."""

import argparse
from datetime import datetime, timezone
import json
import os
from pathlib import Path
import time
import urllib.error
import urllib.parse
import urllib.request

ROOT = Path(__file__).resolve().parents[1]
TAGS = ("latest", "safe", "finalized", "pending")


class RPC:
    def __init__(self, endpoint):
        parsed = urllib.parse.urlparse(endpoint)
        if parsed.scheme != "https" or not parsed.hostname or parsed.username or parsed.password:
            raise ValueError("public HTTPS endpoint required")
        self.endpoint = endpoint
        self.request_id = 0

    def call(self, method, params):
        if method not in {"eth_chainId", "eth_getBlockByNumber"}:
            raise ValueError("prohibited RPC method")
        self.request_id += 1
        body = json.dumps({"jsonrpc": "2.0", "id": self.request_id, "method": method, "params": params}).encode()
        request = urllib.request.Request(
            self.endpoint,
            data=body,
            headers={"Content-Type": "application/json", "User-Agent": "rbh-c04-readonly-probe/1"},
        )
        try:
            with urllib.request.urlopen(request, timeout=15) as response:
                value = json.loads(response.read(4 * 1024 * 1024 + 1))
        except (urllib.error.URLError, TimeoutError, json.JSONDecodeError) as error:
            return {"classification": "TRANSPORT_ERROR", "error_class": type(error).__name__}
        if not isinstance(value, dict) or value.get("id") != self.request_id:
            return {"classification": "MALFORMED"}
        if "error" in value:
            error = value.get("error") or {}
            return {"classification": "UNSUPPORTED", "rpc_code": error.get("code")}
        if "result" not in value:
            return {"classification": "MALFORMED"}
        return {"classification": "SUPPORTED_AND_OBSERVED", "result": value["result"]}


def project_block(value):
    if not isinstance(value, dict):
        return None
    return {key: value.get(key) for key in ("number", "hash", "parentHash", "timestamp")}


def sample(rpc):
    captured = {"captured_at_utc": datetime.now(timezone.utc).isoformat(), "tags": {}}
    for tag in TAGS:
        observed = rpc.call("eth_getBlockByNumber", [tag, False])
        block = project_block(observed.pop("result", None))
        if observed["classification"] == "SUPPORTED_AND_OBSERVED" and block is None:
            observed["classification"] = "MALFORMED"
        observed["block"] = block
        if block and block.get("number") and block.get("hash"):
            canonical = rpc.call("eth_getBlockByNumber", [block["number"], False])
            canonical_block = project_block(canonical.pop("result", None))
            observed["canonical_query_classification"] = canonical["classification"]
            observed["canonical_hash_matches"] = bool(canonical_block and canonical_block.get("hash") == block["hash"])
        captured["tags"][tag] = observed
    return captured


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--allow-readonly-network", action="store_true")
    parser.add_argument("--samples", type=int, default=12)
    parser.add_argument("--interval-seconds", type=float, default=5)
    parser.add_argument("--output", type=Path, default=ROOT / "runs" / "c04-rpc-semantics.json")
    args = parser.parse_args()
    if not args.allow_readonly_network:
        raise SystemExit("explicit --allow-readonly-network is required")
    if args.samples < 2 or args.samples > 100 or args.interval_seconds < 0:
        raise SystemExit("invalid sampling bounds")
    endpoint = os.environ.get("ROBINHOOD_RPC_URL", "")
    if not endpoint:
        raise SystemExit("ROBINHOOD_RPC_URL is required")
    try:
        rpc = RPC(endpoint)
    except ValueError as error:
        raise SystemExit(str(error)) from error
    chain = rpc.call("eth_chainId", [])
    if chain.get("result") != "0x1237":
        raise SystemExit("target chain identity not verified")
    report = {
        "schema_version": 1,
        "scope": "C04_READ_ONLY_RPC_MULTI_SAMPLE",
        "endpoint_class": "public_robinhood_mainnet_rpc",
        "chain_id": 4663,
        "sample_count": args.samples,
        "sample_interval_seconds": args.interval_seconds,
        "samples": [],
        "signed_or_broadcast": False,
        "finality_semantics_verified": False,
        "pending_semantics": "SEMANTICS_NOT_PROVEN",
        "status": "OBSERVED_NOT_SEMANTICALLY_VERIFIED",
    }
    for index in range(args.samples):
        report["samples"].append(sample(rpc))
        if index + 1 < args.samples:
            time.sleep(args.interval_seconds)
    args.output.parent.mkdir(parents=True, exist_ok=True)
    args.output.write_text(json.dumps(report, indent=2) + "\n")
    print(json.dumps({key: report[key] for key in ("scope", "chain_id", "sample_count", "status")}, indent=2))


if __name__ == "__main__":
    main()
