#!/usr/bin/env python3
"""Pinned SDK build/test runner. Use a disposable environment without wallet secrets.
No live-test environment variables are inherited. Upstream tests execute upstream code.
Exit 0=offline checks passed; 1=check failed; 2=blocked. No automatic toolchain download.
"""
import argparse
from datetime import datetime,timezone
import json,os
from pathlib import Path
import shutil,subprocess,sys
ROOT=Path(__file__).resolve().parents[1]
def main():
    p=argparse.ArgumentParser(description=__doc__)
    p.add_argument("--allow-network",action="store_true")
    p.add_argument("--output",type=Path,default=ROOT/"runs"/"sdk-report.json")
    p.add_argument("--work",type=Path,default=ROOT/".work")
    a=p.parse_args(); a.output.parent.mkdir(parents=True,exist_ok=True)
    lock=json.loads((ROOT/"versions.lock.json").read_text())
    report=dict(scope="PINNED_SDK_OFFLINE_CHECKS",created_at_runtime_utc=datetime.now(timezone.utc).isoformat(),
                checks=[],mainnet_validated=False)
    def finish(status,reason,code):
        report.update(status=status,reason=reason)
        a.output.write_text(json.dumps(report,indent=2)+"\n"); print(json.dumps(report,indent=2)); return code
    if not shutil.which("go") or not shutil.which("git"): return finish("BLOCKED","GO_OR_GIT_MISSING",2)
    env={k:os.environ[k] for k in ("PATH","HOME","TMPDIR","SYSTEMROOT","HTTPS_PROXY","HTTP_PROXY","NO_PROXY") if k in os.environ}
    env.update(GOTOOLCHAIN="local",GOWORK="off",GOENV="off")
    v=subprocess.run(["go","version"],capture_output=True,text=True,env=env,timeout=10)
    report.update(observed_go_version=v.stdout.strip(),required_go_version=lock["verification_toolchain"])
    if v.returncode or lock["verification_toolchain"] not in v.stdout.split():
        return finish("BLOCKED","TOOLCHAIN_MISMATCH_NO_AUTO_DOWNLOAD",2)
    if not a.allow_network: return finish("BLOCKED","EXPLICIT_NETWORK_OPT_IN_REQUIRED",2)
    work=a.work.resolve(); work.mkdir(parents=True,exist_ok=True)
    logs=a.output.parent/"sdk-logs"; logs.mkdir(exist_ok=True)
    counter=0
    def run(name,cmd,cwd=None):
        nonlocal counter
        counter+=1; log=logs/f"{counter:02d}-{name}.log"
        try:
            with log.open("w") as stream:
                r=subprocess.run(cmd,cwd=cwd,env=env,stdout=stream,stderr=subprocess.STDOUT,timeout=600)
            rc=r.returncode
        except subprocess.TimeoutExpired: rc=124
        report["checks"].append(dict(name=name,command=cmd,exit_code=rc,log=log.name,status="PASS" if rc==0 else "FAIL"))
        return rc==0
    for name,repo in lock["repositories"].items():
        path=work/f"rbh-{name}-sdk"
        if path.exists():
            head=subprocess.run(["git","-C",str(path),"rev-parse","HEAD"],env=env,capture_output=True,text=True)
            dirty=subprocess.run(["git","-C",str(path),"status","--porcelain"],env=env,capture_output=True,text=True)
            if head.returncode or dirty.returncode or dirty.stdout.strip() or head.stdout.strip()!=repo["commit"]:
                return finish("BLOCKED","EXISTING_SOURCE_NOT_CLEAN_PINNED_COMMIT",2)
        else:
            for label,cmd in [
                ("init",["git","init",str(path)]),
                ("remote",["git","-C",str(path),"remote","add","origin",repo["url"]]),
                ("fetch",["git","-C",str(path),"fetch","--depth=1","origin",repo["commit"]]),
                ("checkout",["git","-C",str(path),"checkout","--detach","FETCH_HEAD"])]:
                if not run(name+"-"+label,cmd): return finish("BLOCKED","PINNED_SOURCE_FETCH_FAILED",2)
        head=subprocess.check_output(["git","-C",str(path),"rev-parse","HEAD"],env=env,text=True).strip()
        if head!=repo["commit"]: return finish("FAIL","COMMIT_MISMATCH",1)
        for filename,key in (("go.mod","go_mod_blob"),("go.sum","go_sum_blob")):
            h=subprocess.check_output(["git","-C",str(path),"hash-object",filename],env=env,text=True).strip()
            if h!=repo[key]: return finish("FAIL","MODULE_BLOB_MISMATCH",1)
        for label,cmd in [
            ("download",["go","mod","download"]),("verify",["go","mod","verify"]),("build",["go","build","./..."]),
            ("test",["go","test","-json","-count=1","./..."]),("race",["go","test","-race","-json","-count=1","./..."]),
            ("vet",["go","vet","./..."])]:
            if not run(name+"-"+label,cmd,path): return finish("FAIL",name+"_"+label+"_FAILED",1)
    spike=work/"spike"; spike.mkdir(exist_ok=True)
    shutil.copy2(ROOT/"spike"/"sdk_smoke_test.go",spike/"sdk_smoke_test.go")
    (spike/"go.mod").write_text("""module example.invalid/rbh-verification
go 1.26.0
toolchain go1.26.6
require (
 github.com/0xfnzero/rbh-parser-sdk v0.0.0
 github.com/0xfnzero/rbh-trade-sdk v0.0.0
 github.com/ethereum/go-ethereum v1.17.5
)
replace github.com/0xfnzero/rbh-parser-sdk => ../rbh-parser-sdk
replace github.com/0xfnzero/rbh-trade-sdk => ../rbh-trade-sdk
""")
    for label,cmd in [
        ("resolve",["go","mod","tidy"]),("dependencies",["go","list","-m","-json","all"]),
        ("smoke",["go","test","-json","-count=1","./..."])]:
        if not run("spike-"+label,cmd,spike): return finish("FAIL","SPIKE_"+label+"_FAILED",1)
    for name in ("go.mod","go.sum"): shutil.copy2(spike/name,a.output.parent/("resolved-spike-"+name))
    counts=dict(pass_count=0,fail_count=0,skip_count=0)
    for f in logs.glob("*.log"):
        for line in f.read_text(errors="replace").splitlines():
            try: e=json.loads(line)
            except ValueError: continue
            if isinstance(e,dict) and e.get("Test") and e.get("Action") in ("pass","fail","skip"):
                counts[e["Action"]+"_count"]+=1
    report["go_test_events"]=counts
    return finish("PASS_OFFLINE_ONLY","NO_MAINNET_OR_HISTORICAL_VALIDATION",0)
if __name__=="__main__": sys.exit(main())
