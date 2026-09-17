#!/usr/bin/env python3
"""Read-only discovery of failed transactions targeting known Pons Curve-path contracts."""
import argparse
import json
import os
import time
import urllib.error
import urllib.request

TARGETS = {
    "0x4197749b1b3bbb9e1d8a183f968cc54c42e882a7",  # fixture curve
    "0x7ed598bcef8bd9edd8c97a195c6d13f40801ec7e",  # Pons factory
    "0xe33e9e479df8802cb0866d5d05258bec4cf62948",  # launch-and-buy
}


def call(url, payload):
    request = urllib.request.Request(
        url,
        data=json.dumps(payload).encode(),
        headers={"Content-Type": "application/json", "User-Agent": "rbh-validation-package/1"},
    )
    for attempt in range(4):
        try:
            with urllib.request.urlopen(request, timeout=30) as response:
                return json.loads(response.read())
        except urllib.error.HTTPError as error:
            if error.code != 429 or attempt == 3:
                raise
            time.sleep(2 ** attempt)


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--allow-readonly-network", action="store_true")
    parser.add_argument("--from-block", type=int, required=True)
    parser.add_argument("--to-block", type=int, required=True)
    parser.add_argument("--batch-size", type=int, default=100)
    args = parser.parse_args()
    if not args.allow_readonly_network:
        raise SystemExit("explicit --allow-readonly-network is required")
    url = os.environ.get("ROBINHOOD_RPC_URL")
    if not url:
        raise SystemExit("ROBINHOOD_RPC_URL is required")
    candidates = []
    request_id = 0
    for start in range(args.from_block, args.to_block + 1, args.batch_size):
        batch = []
        for number in range(start, min(start + args.batch_size, args.to_block + 1)):
            request_id += 1
            batch.append({"jsonrpc": "2.0", "id": request_id, "method": "eth_getBlockByNumber", "params": [hex(number), True]})
        replies = call(url, batch)
        for reply in replies:
            block = reply.get("result")
            if not isinstance(block, dict):
                continue
            for tx in block.get("transactions", []):
                if (tx.get("to") or "").lower() in TARGETS:
                    candidates.append((int(block["number"], 16), tx["hash"], tx["to"].lower()))
    failed = []
    for block_number, tx_hash, target in candidates:
        request_id += 1
        reply = call(url, {"jsonrpc": "2.0", "id": request_id, "method": "eth_getTransactionReceipt", "params": [tx_hash]})
        receipt = reply.get("result")
        if isinstance(receipt, dict) and receipt.get("status") == "0x0":
            failed.append({"block_number": block_number, "tx_hash": tx_hash, "target": target})
    print(json.dumps({"scope": [args.from_block, args.to_block], "targeted_transactions": len(candidates), "failed": failed}, indent=2))


if __name__ == "__main__":
    main()
