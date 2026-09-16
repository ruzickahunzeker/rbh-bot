"""Local contract/durability reference model.
No RPC, keys, real signing, encryption or EVM transactions.
MODEL_ONLY payload hashes are SHA256 test identifiers, NOT Ethereum transaction hashes.
"""
import hashlib
import json
import re
import sqlite3
from contextlib import contextmanager

PREFIX=b"MODEL_ONLY_NOT_AN_EVM_TRANSACTION:"
MAX=(1<<256)-1
class Reject(ValueError): pass
class Conflict(Reject): pass
class LPDisabled(Reject): pass

def amount(v, positive=False):
    if not isinstance(v,str) or not re.fullmatch(r"0|[1-9][0-9]{0,77}",v):
        raise Reject("canonical integer string required")
    n=int(v)
    if n>MAX or (positive and n==0): raise Reject("amount out of range")
    return n

def address(v):
    if not isinstance(v,str) or not re.fullmatch(r"0x[0-9a-fA-F]{40}",v):
        raise Reject("invalid address")
    return v.lower()  # Canonicalization, NOT EIP-55 validation.

def digest(v):
    return hashlib.sha256(json.dumps(v,sort_keys=True,separators=(",",":")).encode()).hexdigest()

def action_id(chain,strategy,wallet,tx,path):
    if type(chain) is not int or chain<=0 or not all(isinstance(x,str) and x for x in (strategy,wallet,path)):
        raise Reject("invalid action")
    if not re.fullmatch(r"0x[0-9a-fA-F]{64}",tx): raise Reject("invalid tx hash")
    return digest([chain,strategy,wallet,tx.lower(),path])

def lp_capabilities():
    return dict(observe_pool_liquidity="unknown",observe_wallet_position="unknown",execute="not_implemented")

def request(**kw):
    r=dict(kind="swap",chain_id=4663,wallet="test-wallet",strategy="test-strategy",
           asset_in="0x"+"00"*20,asset_out="0x"+"ab"*20,amount_in="10",minimum_out="1",mode="dry_run")
    r.update(kw)
    return r

def normalize(r):
    if str(r.get("kind","")).startswith("liquidity."): raise LPDisabled("LP_EXECUTION_NOT_IMPLEMENTED")
    if set(r)!=set(request()): raise Reject("unknown/missing fields")
    if r["kind"]!="swap" or r["mode"]!="dry_run": raise Reject("only reference dry_run swap")
    if type(r["chain_id"]) is not int or r["chain_id"]!=4663: raise Reject("wrong chain")
    if not all(isinstance(r[k],str) and 0<len(r[k])<=128 for k in ("wallet","strategy")):
        raise Reject("invalid owner")
    out=dict(r)
    out["asset_in"]=address(r["asset_in"]); out["asset_out"]=address(r["asset_out"])
    if out["asset_in"]==out["asset_out"]: raise Reject("identical assets")
    amount(r["amount_in"],True); amount(r["minimum_out"],True)
    return out

def check_snapshot(s,epoch,block_hash,now):
    if s.get("verified") is not True: raise Reject("unverified")
    if s.get("epoch")!=epoch or s.get("block_hash")!=block_hash: raise Reject("orphaned")
    if s.get("expires",0)<=now: raise Reject("expired")
    if not s.get("route_version") or not s.get("state_version"): raise Reject("version missing")

class DB:
    def __init__(self,path):
        self.db=sqlite3.connect(str(path),isolation_level=None,timeout=.2)
        self.db.row_factory=sqlite3.Row
        for p in ("journal_mode=WAL","synchronous=FULL","foreign_keys=ON","busy_timeout=200"):
            self.db.execute("PRAGMA "+p)
    @contextmanager
    def tx(self):
        self.db.execute("BEGIN IMMEDIATE")
        try: yield self.db
        except BaseException:
            self.db.execute("ROLLBACK"); raise
        else: self.db.execute("COMMIT")
    def close(self): self.db.close()

class Store(DB):
    """Simplified single-chain, single-step journal. Not a production executor."""
    def __init__(self,path):
        super().__init__(path)
        self.db.executescript("""
        CREATE TABLE IF NOT EXISTS ops(id TEXT PRIMARY KEY,wallet TEXT,idem TEXT,fp TEXT,UNIQUE(wallet,idem));
        CREATE TABLE IF NOT EXISTS steps(id TEXT PRIMARY KEY,op TEXT REFERENCES ops(id),wallet TEXT,nonce INTEGER,state TEXT,
          UNIQUE(wallet,nonce));
        CREATE TABLE IF NOT EXISTS artifacts(step TEXT PRIMARY KEY REFERENCES steps(id),hash TEXT,payload BLOB);
        CREATE TABLE IF NOT EXISTS attempts(id INTEGER PRIMARY KEY,step TEXT REFERENCES steps(id),hash TEXT);
        CREATE TABLE IF NOT EXISTS balances(wallet TEXT,asset TEXT,amount TEXT,PRIMARY KEY(wallet,asset));
        CREATE TABLE IF NOT EXISTS reservations(op TEXT REFERENCES ops(id),asset TEXT,amount TEXT,PRIMARY KEY(op,asset));
        CREATE TABLE IF NOT EXISTS flags(id INTEGER PRIMARY KEY,halt INTEGER);
        INSERT OR IGNORE INTO flags VALUES(1,0);
        """)
    def admit(self,key,r):
        n=normalize(r)
        if not isinstance(key,str) or not 0<len(key)<=256: raise Reject("invalid key")
        fp=digest(n); wallet=n["wallet"]
        with self.tx() as c:
            old=c.execute("SELECT * FROM ops WHERE wallet=? AND idem=?",(wallet,key)).fetchone()
            if old:
                if old["fp"]!=fp: raise Conflict("IDEMPOTENCY_CONFLICT")
                return old["id"]
            op=digest([wallet,key])
            c.execute("INSERT INTO ops VALUES(?,?,?,?)",(op,wallet,key,fp))
            return op
    def balance(self,wallet,asset,value):
        amount(value); asset=address(asset)
        with self.tx() as c: c.execute("INSERT OR REPLACE INTO balances VALUES(?,?,?)",(wallet,asset,value))
    def reserve(self,op,budgets):
        items={address(k):str(amount(v,True)) for k,v in budgets.items()}
        if not items or len(items)!=len(budgets): raise Reject("invalid budgets")
        with self.tx() as c:
            row=c.execute("SELECT wallet FROM ops WHERE id=?",(op,)).fetchone()
            if not row: raise Reject("unknown operation")
            old={r["asset"]:r["amount"] for r in c.execute("SELECT * FROM reservations WHERE op=?",(op,))}
            if old:
                if old!=items: raise Conflict("budget changed")
                return
            for asset,value in items.items():
                bal=c.execute("SELECT amount FROM balances WHERE wallet=? AND asset=?",(row["wallet"],asset)).fetchone()
                used=sum(int(r[0]) for r in c.execute("""SELECT r.amount FROM reservations r JOIN ops o ON o.id=r.op
                  WHERE o.wallet=? AND r.asset=?""",(row["wallet"],asset)))
                if not bal or int(value)>int(bal[0])-used: raise Reject("insufficient unreserved balance")
            for asset,value in items.items(): c.execute("INSERT INTO reservations VALUES(?,?,?)",(op,asset,value))
    def prepare(self,op,nonce):
        if type(nonce) is not int or not 0<=nonce<(1<<63): raise Reject("invalid model nonce")
        with self.tx() as c:
            row=c.execute("SELECT wallet FROM ops WHERE id=?",(op,)).fetchone()
            if not row: raise Reject("unknown operation")
            old=c.execute("SELECT * FROM steps WHERE op=?",(op,)).fetchone()
            if old:
                if old["nonce"]!=nonce: raise Conflict("nonce already bound")
                return old["id"]
            if c.execute("SELECT halt FROM flags").fetchone()[0]: raise Reject("halted")
            if c.execute("SELECT 1 FROM steps WHERE wallet=? AND state!='included'",(row["wallet"],)).fetchone():
                raise Reject("unresolved wallet lane")
            step=digest([op,0])
            c.execute("INSERT INTO steps VALUES(?,?,?,?,?)",(step,op,row["wallet"],nonce,"prepared"))
            return step
    def artifact(self,step,payload):
        if not isinstance(payload,bytes) or not payload.startswith(PREFIX): raise Reject("model payload required")
        h=hashlib.sha256(payload).hexdigest()
        with self.tx() as c:
            old=c.execute("SELECT * FROM artifacts WHERE step=?",(step,)).fetchone()
            if old:
                if old["hash"]!=h: raise Conflict("replacement not implemented")
                return h
            c.execute("INSERT INTO artifacts VALUES(?,?,?)",(step,h,payload))
            c.execute("UPDATE steps SET state='signed_model' WHERE id=?",(step,))
        return h
    def attempt(self,step):
        with self.tx() as c:
            if c.execute("SELECT halt FROM flags").fetchone()[0]: raise Reject("halted")
            row=c.execute("SELECT s.*,a.hash,a.payload FROM steps s JOIN artifacts a ON a.step=s.id WHERE s.id=?",(step,)).fetchone()
            if not row: raise Reject("durable artifact required")
            c.execute("INSERT INTO attempts(step,hash) VALUES(?,?)",(step,row["hash"]))
            c.execute("UPDATE steps SET state='broadcast_unknown' WHERE id=?",(step,))
            return dict(row)
    def reconcile(self,step,chain):
        a=self.db.execute("SELECT hash FROM artifacts WHERE step=?",(step,)).fetchone()
        state="included" if a and chain.contains(a[0]) else "broadcast_unknown"
        with self.tx() as c: c.execute("UPDATE steps SET state=? WHERE id=?",(state,step))
        return state
    def halt(self): self.db.execute("UPDATE flags SET halt=1")

class FakeChain(DB):
    """Local test double with idempotent effects. It is NOT EVM evidence."""
    def __init__(self,path):
        super().__init__(path)
        self.db.execute("CREATE TABLE IF NOT EXISTS effects(wallet TEXT,nonce INTEGER,hash TEXT UNIQUE,PRIMARY KEY(wallet,nonce))")
    def send(self,a):
        if not a["payload"].startswith(PREFIX): raise Reject("model only")
        with self.tx() as c:
            old=c.execute("SELECT hash FROM effects WHERE wallet=? AND nonce=?",(a["wallet"],a["nonce"])).fetchone()
            if old and old[0]!=a["hash"]: raise Conflict("unapproved replacement")
            c.execute("INSERT OR IGNORE INTO effects VALUES(?,?,?)",(a["wallet"],a["nonce"],a["hash"]))
    def contains(self,h): return bool(self.db.execute("SELECT 1 FROM effects WHERE hash=?",(h,)).fetchone())

class Feed(DB):
    """Tests event/outbox/cursor atomicity, NOT the real SDK registry staging."""
    def __init__(self,path):
        super().__init__(path)
        self.db.executescript("""
        CREATE TABLE IF NOT EXISTS events(offset INTEGER PRIMARY KEY AUTOINCREMENT,obs TEXT UNIQUE,payload TEXT);
        CREATE TABLE IF NOT EXISTS outbox(offset INTEGER PRIMARY KEY REFERENCES events(offset),payload TEXT);
        CREATE TABLE IF NOT EXISTS cursor(id INTEGER PRIMARY KEY,value INTEGER);
        INSERT OR IGNORE INTO cursor VALUES(1,0);
        """)
        self.memory=self.db.execute("SELECT value FROM cursor").fetchone()[0]
    def commit(self,obs,payload,fault=""):
        with self.tx() as c:
            old=c.execute("SELECT offset FROM events WHERE obs=?",(obs,)).fetchone()
            if old: return old[0]
            raw=json.dumps(payload)
            offset=c.execute("INSERT INTO events(obs,payload) VALUES(?,?)",(obs,raw)).lastrowid
            if fault=="before_outbox": raise RuntimeError("injected failure")
            c.execute("INSERT INTO outbox VALUES(?,?)",(offset,raw))
            c.execute("UPDATE cursor SET value=?",(offset,))
        if fault=="after_commit": raise RuntimeError("injected failure")
        self.memory=offset
        return offset

class Journal(DB):
    """Single-asset reversible-delta model; no real receipts or LP accounting."""
    def __init__(self,path):
        super().__init__(path)
        self.db.execute("CREATE TABLE IF NOT EXISTS entries(id TEXT PRIMARY KEY,block TEXT,delta TEXT,reversal TEXT UNIQUE)")
    def apply(self,key,block,delta):
        if type(delta) is not int: raise Reject("integer delta")
        with self.tx() as c:
            old=c.execute("SELECT * FROM entries WHERE id=?",(key,)).fetchone()
            if old and (old["block"]!=block or old["delta"]!=str(delta)): raise Conflict("identity changed")
            c.execute("INSERT OR IGNORE INTO entries VALUES(?,?,?,NULL)",(key,block,str(delta)))
    def orphan(self,block):
        with self.tx() as c:
            for r in c.execute("SELECT * FROM entries WHERE block=? AND reversal IS NULL",(block,)).fetchall():
                c.execute("INSERT OR IGNORE INTO entries VALUES(?,?,?,?)",("undo:"+r["id"],block,str(-int(r["delta"])),r["id"]))
    def total(self): return sum(int(r[0]) for r in self.db.execute("SELECT delta FROM entries"))
