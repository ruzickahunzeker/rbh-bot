#!/usr/bin/env python3
"""Opt-in READ-ONLY RPC probe and receipt capture. No signing or eth_send* calls.
Method success does NOT validate finality, simulation or pending-state semantics.
"""
import argparse
from datetime import datetime,timezone
import json,os
from pathlib import Path
import re,sys
import urllib.request,urllib.parse,urllib.error
ROOT=Path(__file__).resolve().parents[1]
ALLOWED={"eth_chainId","eth_getBlockByNumber","eth_getBlockByHash","eth_getTransactionByHash","eth_getTransactionReceipt"}
class Probe:
    def __init__(self,url):
        p=urllib.parse.urlparse(url)
        if p.scheme not in ("http","https") or not p.hostname or p.username or p.password: raise ValueError("invalid endpoint")
        if p.scheme=="http" and p.hostname not in ("localhost","127.0.0.1","::1"): raise ValueError("HTTPS required")
        self.url=url; self.i=0
    def call(self,method,params):
        if method not in ALLOWED: raise ValueError("RPC method prohibited")
        self.i+=1
        r=urllib.request.Request(self.url,data=json.dumps(dict(jsonrpc="2.0",id=self.i,method=method,params=params)).encode(),
                                 headers={"Content-Type":"application/json","User-Agent":"rbh-validation-package/1"})
        try:
            with urllib.request.urlopen(r,timeout=12) as response: raw=response.read(16*1024*1024+1)
            if len(raw)>16*1024*1024: return dict(status="RESPONSE_TOO_LARGE")
            obj=json.loads(raw)
            if not isinstance(obj,dict) or obj.get("id")!=self.i: return dict(status="MALFORMED_RESPONSE")
            if "error" in obj: return dict(status="RPC_ERROR",code=obj["error"].get("code"))
            if "result" not in obj: return dict(status="MALFORMED_RESPONSE")
            return dict(status="RESPONSE_RECEIVED",result=obj["result"])
        except urllib.error.HTTPError as e: return dict(status="HTTP_ERROR",code=e.code)
        except Exception as e:
            # Never persist exception text, which may contain credential-bearing URLs.
            return dict(status="TRANSPORT_OR_DECODE_ERROR",error_class=type(e).__name__)
def main():
    p=argparse.ArgumentParser(description=__doc__)
    p.add_argument("--allow-readonly-network",action="store_true")
    p.add_argument("--fixture-manifest",type=Path)
    p.add_argument("--output",type=Path,default=ROOT/"runs"/"rpc-report.json")
    a=p.parse_args(); a.output.parent.mkdir(parents=True,exist_ok=True)
    data=dict(scope="READ_ONLY_RPC",created_at_runtime_utc=datetime.now(timezone.utc).isoformat(),
              checks={},signed_or_broadcast=False,finality_semantics_verified=False)
    def finish(status,reason,code):
        data.update(status=status,reason=reason); a.output.write_text(json.dumps(data,indent=2)+"\n")
        print(json.dumps(data,indent=2)); return code
    if not a.allow_readonly_network: return finish("BLOCKED","EXPLICIT_READONLY_OPT_IN_REQUIRED",2)
    url=os.environ.get("ROBINHOOD_RPC_URL","")
    if not url: return finish("BLOCKED","RPC_ENDPOINT_NOT_CONFIGURED",2)
    try: probe=Probe(url)
    except ValueError: return finish("BLOCKED","INVALID_ENDPOINT",2)
    r=probe.call("eth_chainId",[]); data["checks"]["eth_chainId"]=r
    if r.get("status")!="RESPONSE_RECEIVED": return finish("BLOCKED","CHAIN_ID_READ_FAILED",2)
    try: chain=int(r.get("result"),16)
    except (ValueError,TypeError): return finish("FAIL","MALFORMED_CHAIN_ID",1)
    if chain!=4663: return finish("FAIL","WRONG_CHAIN",1)
    for tag in ("latest","safe","finalized","pending"):
        out=probe.call("eth_getBlockByNumber",[tag,False])
        if isinstance(out.get("result"),dict):
            out["result"]={k:out["result"].get(k) for k in ("number","hash","parentHash","timestamp")}
        data["checks"]["block_tag_"+tag]=out
    if a.fixture_manifest:
        m=json.loads(a.fixture_manifest.read_text())
        if m.get("provenance")=="SYNTHETIC": return finish("FAIL","SYNTHETIC_INPUT_PROHIBITED",1)
        items=m.get("transactions",[])
        data["fixtures_status"]="INCOMPLETE_NO_TRANSACTION_HASHES" if not items else "CAPTURE_ATTEMPTED"
        saved=0; errors=[]
        for item in items:
            h=item.get("tx_hash","")
            if not isinstance(h,str) or not re.fullmatch(r"0x[0-9a-fA-F]{64}",h):
                errors.append("INVALID_HASH"); continue
            receipt=probe.call("eth_getTransactionReceipt",[h]).get("result")
            tx=probe.call("eth_getTransactionByHash",[h]).get("result")
            if not isinstance(receipt,dict) or not isinstance(tx,dict) or receipt.get("transactionHash","").lower()!=h.lower():
                errors.append("MISSING_OR_MISMATCHED_RECEIPT"); continue
            block=probe.call("eth_getBlockByHash",[receipt.get("blockHash"),False]).get("result")
            current=probe.call("eth_getBlockByNumber",[receipt.get("blockNumber"),False]).get("result")
            if not isinstance(current,dict) or not isinstance(block,dict) or current.get("hash")!=receipt.get("blockHash"):
                errors.append("CANONICALITY_UNVERIFIED"); continue
            target=a.output.parent/"historical-captures"; target.mkdir(exist_ok=True)
            obj=dict(provenance="RPC_CAPTURE_NOT_GOLDEN_EXPECTATION",chain_id=4663,tx_hash=h,transaction=tx,
                     receipt=receipt,block=block,canonical_at_read=True,pre_transaction_state_verified=False)
            (target/(h+".json")).write_text(json.dumps(obj,indent=2)+"\n"); saved+=1
        data.update(fixtures_saved=saved,fixture_failures=errors)
    return finish("OBSERVED_NOT_SEMANTICALLY_VERIFIED","INSPECT_RESULTS_AND_COVERAGE",0)
if __name__=="__main__": sys.exit(main())
