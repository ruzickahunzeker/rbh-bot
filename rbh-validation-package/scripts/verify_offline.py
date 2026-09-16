#!/usr/bin/env python3
"""Offline reference-model checks, not SDK/EVM validation."""
import argparse
from datetime import datetime,timezone
import io,json
from pathlib import Path
import platform,sqlite3,sys,time,unittest
ROOT=Path(__file__).resolve().parents[1]
sys.path.insert(0,str(ROOT))
class Result(unittest.TextTestResult):
    def __init__(self,*a,**kw): super().__init__(*a,**kw); self.records=[]; self.starts={}
    def startTest(self,t): self.starts[t.id()]=time.monotonic(); super().startTest(t)
    def record(self,t,status,detail=None):
        self.records.append(dict(id=t.id(),status=status,detail=detail,duration_ms=round((time.monotonic()-self.starts[t.id()])*1000,3)))
    def addSuccess(self,t): super().addSuccess(t); self.record(t,"PASS")
    def addFailure(self,t,e): super().addFailure(t,e); self.record(t,"FAIL",self._exc_info_to_string(e,t))
    def addError(self,t,e): super().addError(t,e); self.record(t,"ERROR",self._exc_info_to_string(e,t))
    def addSkip(self,t,why): super().addSkip(t,why); self.record(t,"SKIP",why)
    def addSubTest(self,t,sub,e):
        super().addSubTest(t,sub,e)
        if e: self.record(t,"FAIL_SUBTEST",self._exc_info_to_string(e,t))
def main():
    p=argparse.ArgumentParser(description=__doc__)
    p.add_argument("--output",type=Path,default=ROOT/"runs"/"offline-results.json")
    a=p.parse_args(); a.output.parent.mkdir(parents=True,exist_ok=True)
    suite=unittest.defaultTestLoader.discover(str(ROOT/"verification"),pattern="test_*.py",top_level_dir=str(ROOT))
    stream=io.StringIO(); stamp=datetime.now(timezone.utc).isoformat()
    r=unittest.TextTestRunner(stream=stream,verbosity=2,resultclass=Result).run(suite)
    data=dict(scope="OFFLINE_REFERENCE_MODEL_ONLY",created_at_runtime_utc=stamp,
              python=platform.python_version(),sqlite=sqlite3.sqlite_version,
              tests=r.testsRun,failures=len(r.failures),errors=len(r.errors),skipped=len(r.skipped),
              status="PASS" if r.wasSuccessful() else "FAIL",production_ready=False,
              sdk_tests_executed=False,mainnet_tests_executed=False,records=r.records)
    a.output.write_text(json.dumps(data,indent=2)+"\n")
    a.output.with_suffix(".log").write_text(stream.getvalue())
    print(stream.getvalue()); print(json.dumps({k:v for k,v in data.items() if k!="records"},indent=2))
    return 0 if r.wasSuccessful() else 1
if __name__=="__main__": sys.exit(main())
