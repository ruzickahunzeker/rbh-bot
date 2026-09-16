#!/usr/bin/env python3
"""Verify the delivered manifest, JSON syntax and internal evidence consistency."""
import hashlib,json
from pathlib import Path
import sys
ROOT=Path(__file__).resolve().parents[1]
def main():
    errors=[]; checked=0
    manifest=ROOT/"SHA256SUMS"
    if not manifest.exists():
        print("SHA256SUMS missing"); return 1
    for line in manifest.read_text().splitlines():
        if not line: continue
        try: expected,name=line.split("  ",1)
        except ValueError:
            errors.append("malformed manifest"); continue
        p=(ROOT/name).resolve()
        if ROOT not in p.parents or not p.is_file():
            errors.append("missing or invalid path: "+name); continue
        if hashlib.sha256(p.read_bytes()).hexdigest()!=expected: errors.append("hash mismatch: "+name)
        checked+=1
        if p.suffix==".json":
            try: json.loads(p.read_text())
            except ValueError: errors.append("invalid JSON: "+name)
    r=json.loads((ROOT/"evidence"/"offline-results.json").read_text())
    if r.get("scope")!="OFFLINE_REFERENCE_MODEL_ONLY" or r.get("status")!="PASS":
        errors.append("unexpected model report scope/status")
    if r.get("tests")!=39 or r.get("failures") or r.get("errors") or r.get("skipped"):
        errors.append("unexpected model test counts")
    if len(r.get("records",[]))!=39 or any(x.get("status")!="PASS" for x in r.get("records",[])):
        errors.append("inconsistent per-test records")
    if r.get("production_ready") or r.get("sdk_tests_executed") or r.get("mainnet_tests_executed"):
        errors.append("reference model mislabeled as real integration")
    cap=json.loads((ROOT/"contracts"/"capabilities.json").read_text())
    if cap["lp"]["execute"]!="not_implemented" or cap["live"]:
        errors.append("LP/live scope mismatch")
    print(json.dumps(dict(status="PASS" if not errors else "FAIL",files_checked=checked,errors=errors),indent=2))
    return 1 if errors else 0
if __name__=="__main__": sys.exit(main())
