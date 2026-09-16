"""Crash worker: local dummy chain only."""
import os
from pathlib import Path
import sys
from verification.model import Store,FakeChain,PREFIX,request
root=Path(sys.argv[1]); stage=sys.argv[2]
s=Store(root/"trade.db"); c=FakeChain(root/"fake-chain.db")
op=s.admit("crash",request()); step=s.prepare(op,0)
s.artifact(step,PREFIX+b"crash")
a=s.attempt(step)
if stage=="before_send": os._exit(44)
c.send(a)
if stage=="after_send": os._exit(44)
s.reconcile(step,c)
if stage=="after_reconcile": os._exit(44)
raise ValueError("unknown stage")
