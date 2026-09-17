#!/usr/bin/env python3
"""Verify Pons Curve expectations directly from raw ABI words; never imports parser-sdk."""
import json
from pathlib import Path
import sys

ROOT = Path(__file__).resolve().parents[1]
CANDIDATES = ROOT / "fixtures" / "historical" / "candidates"
EXPECTED = ROOT / "fixtures" / "historical" / "expected"
LAUNCH = "0x8d4aad4953d0ca700d468f3753aa14432d1b35b43ec6409f051fb6aa43a89607"
BUY = "0xec36bf571f136799e8dc0b0b8bea4b04d8bd3d43de838aab0d5fc21d4cbfc455"
SELL = "0x8113d738abdcb6b38357e9d53a54a7157861a09031b453651f0fe7fe151f59df"


def address(word):
    if not isinstance(word, str) or len(word) != 66 or not word.startswith("0x"):
        raise ValueError("invalid ABI address word")
    return "0x" + word[-40:].lower()


def words(data):
    if not isinstance(data, str) or not data.startswith("0x") or (len(data) - 2) % 64:
        raise ValueError("invalid ABI data")
    raw = data[2:]
    return [str(int(raw[i:i + 64], 16)) for i in range(0, len(raw), 64)]


def decoded(log):
    topic = log["topics"][0].lower()
    base = {
        "source_log_index": int(log["logIndex"], 16),
        "source_topic0": topic,
        "protocol": "pons-v2",
    }
    values = words(log["data"])
    if topic == LAUNCH and len(log["topics"]) == 4 and len(values) == 3:
        return base | {
            "kind": "launch",
            "token": address(log["topics"][1]),
            "curve": address(log["topics"][2]),
            "creator": address(log["topics"][3]),
            "quote": address("0x" + log["data"][2:66]),
            "launch_config_id": values[1],
            "graduation_threshold": values[2],
        }
    if topic in (BUY, SELL) and len(log["topics"]) == 3 and len(values) == 4:
        return base | {
            "kind": "curve-buy" if topic == BUY else "curve-sell",
            "buyer_or_seller": address(log["topics"][1]),
            "recipient": address(log["topics"][2]),
            "amount_in": values[0],
            "amount_out": values[1],
            "fee": values[2],
            "tax": values[3],
        }
    raise ValueError("unsupported expected event ABI")


def main():
    checked = 0
    errors = []
    for path in sorted(EXPECTED.glob("*.json")):
        expectation = json.loads(path.read_text())
        if expectation.get("provenance") == "HISTORICAL_CHAIN_NEGATIVE":
            tx_hash = expectation.get("tx_hash", "").lower()
            capture_path = CANDIDATES / f"{tx_hash}.json"
            if not capture_path.exists():
                errors.append(f"{path.name}: negative capture missing")
                continue
            capture = json.loads(capture_path.read_text())
            receipt = capture.get("receipt", {})
            transaction = capture.get("transaction", {})
            block = capture.get("block", {})
            expected = expectation.get("expected", {})
            actual = {
                "chain_id": capture.get("chain_id"),
                "canonical": capture.get("canonical_at_read"),
                "status": int(receipt.get("status", "-1"), 16),
                "block_number": int(receipt.get("blockNumber", "0x0"), 16),
                "block_hash": receipt.get("blockHash", "").lower(),
                "transaction_index": int(receipt.get("transactionIndex", "0x0"), 16),
                "to": (transaction.get("to") or "").lower(),
                "selector": transaction.get("input", "")[:10].lower(),
                "logs": len(receipt.get("logs", [])),
                "block_matches": block.get("hash", "").lower() == receipt.get("blockHash", "").lower(),
            }
            required = {
                "chain_id": expectation.get("chain_id"), "canonical": True,
                "status": expectation.get("receipt_status"), "block_number": expectation.get("block_number"),
                "block_hash": expectation.get("block_hash", "").lower(),
                "transaction_index": expectation.get("transaction_index"), "to": expectation.get("to", "").lower(),
                "selector": expectation.get("calldata_selector", "").lower(), "logs": 0, "block_matches": True,
            }
            safety = expected == {"audit_reason": "receipt_reverted", "audit_persisted": True,
                                   "normalized_economic_events": 0, "copy_eligible": False,
                                   "parser_invoked": False, "parser_registry_mutated": False}
            if actual != required or not safety:
                errors.append(f"{path.name}: historical negative mismatch")
            checked += 1
            continue
        if expectation.get("provenance") != "INDEPENDENT_RAW_LOG_ABI_REVIEW":
            errors.append(f"{path.name}: invalid provenance")
            continue
        tx_hash = expectation.get("tx_hash", "").lower()
        capture_path = CANDIDATES / f"{tx_hash}.json"
        if not capture_path.exists():
            errors.append(f"{path.name}: capture missing")
            continue
        capture = json.loads(capture_path.read_text())
        receipt = capture.get("receipt", {})
        if capture.get("canonical_at_read") is not True or receipt.get("status") != "0x1":
            errors.append(f"{path.name}: receipt is not canonical successful history")
            continue
        logs = {int(log["logIndex"], 16): log for log in receipt.get("logs", [])}
        for expected in expectation.get("events", []):
            index = expected.get("source_log_index")
            try:
                actual = decoded(logs[index])
            except (KeyError, TypeError, ValueError) as exc:
                errors.append(f"{path.name}:{index}: {exc}")
                continue
            if actual != expected:
                errors.append(f"{path.name}:{index}: raw-log expectation mismatch")
            checked += 1
    print(json.dumps({"status": "PASS" if not errors else "FAIL", "events_checked": checked, "errors": errors}, indent=2))
    return 1 if errors else 0


if __name__ == "__main__":
    sys.exit(main())
