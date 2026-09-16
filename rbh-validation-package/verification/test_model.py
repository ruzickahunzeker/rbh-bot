"""Contract-model tests only. No SDK, signing or network calls."""
import os
from pathlib import Path
import sqlite3
import subprocess
import sys
import tempfile
import unittest
from concurrent.futures import ThreadPoolExecutor
from verification.model import *

ROOT=Path(__file__).resolve().parents[1]
A0="0x"+"00"*20; A1="0x"+"ab"*20
H1="0x"+"11"*32; H2="0x"+"22"*32

class ContractTests(unittest.TestCase):
    def test_01_uint256_boundaries(self):
        self.assertEqual(amount(str(MAX)),MAX)
        self.assertEqual(amount("0"),0)
        with self.assertRaises(Reject): amount(str(MAX+1))
    def test_02_reject_noncanonical_amounts(self):
        for v in [1.1,1,True,"-1","+1","1e18","0.1","01"," 1","","1"*79]:
            with self.subTest(value=v),self.assertRaises(Reject): amount(v)
    def test_03_address_width_and_normalization(self):
        self.assertEqual(address("0x"+"AB"*20),A1)
        with self.assertRaises(Reject): address("0x12")
    def test_04_request_canonicalization(self):
        self.assertEqual(normalize(request(asset_out="0x"+"AB"*20)),request())
    def test_05_zero_minout_rejected(self):
        with self.assertRaises(Reject): normalize(request(minimum_out="0"))
    def test_06_zero_input_rejected(self):
        with self.assertRaises(Reject): normalize(request(amount_in="0"))
    def test_07_untrusted_execution_fields_rejected(self):
        for k in ["router","hook","pool_key","calldata","nonce"]:
            with self.subTest(field=k),self.assertRaises(Reject): normalize(request(**{k:"bad"}))
    def test_08_no_live_mode_in_model(self):
        with self.assertRaises(Reject): normalize(request(mode="live"))
    def test_09_lp_actions_hard_rejected(self):
        for k in ["open","increase","decrease","collect","close","rebalance"]:
            with self.subTest(action=k),self.assertRaises(LPDisabled): normalize(request(kind="liquidity."+k))
    def test_10_lp_observation_not_claimed_supported(self):
        self.assertEqual(lp_capabilities(),dict(observe_pool_liquidity="unknown",observe_wallet_position="unknown",execute="not_implemented"))
    def test_11_environment_cannot_enable_lp(self):
        os.environ["LP_EXECUTE"]="true"
        try:
            with self.assertRaises(LPDisabled): normalize(request(kind="liquidity.open"))
        finally: del os.environ["LP_EXECUTE"]
    def test_12_cross_provider_logical_action_dedupe(self):
        ids=[action_id(4663,"s","w",H1,"swap/0") for _ in ["sequencer","blockrazor","receipt"]]
        self.assertEqual(len(set(ids)),1)
    def test_13_distinct_action_paths(self):
        self.assertNotEqual(action_id(4663,"s","w",H1,"swap/0"),action_id(4663,"s","w",H1,"swap/1"))
    def test_14_wrong_chain_rejected(self):
        for v in [1,"4663",True]:
            with self.subTest(value=v),self.assertRaises(Reject): normalize(request(chain_id=v))
    def test_15_quote_epoch_block_and_expiry(self):
        s=dict(verified=True,epoch=3,block_hash=H1,expires=100,route_version="r",state_version="s")
        check_snapshot(s,3,H1,99)
        for args in [(4,H1,99),(3,H2,99),(3,H1,100)]:
            with self.subTest(args=args),self.assertRaises(Reject): check_snapshot(s,*args)
    def test_16_unverified_route_rejected(self):
        with self.assertRaises(Reject): check_snapshot({},1,H1,0)

class StoreTests(unittest.TestCase):
    def setUp(self):
        self.temp=tempfile.TemporaryDirectory(); self.p=Path(self.temp.name)
        self.s=Store(self.p/"trade.db"); self.c=FakeChain(self.p/"fake-chain.db")
    def tearDown(self):
        self.s.close(); self.c.close(); self.temp.cleanup()
    def step(self):
        op=self.s.admit("one",request()); return op,self.s.prepare(op,3)
    def test_17_wal_full_foreign_keys(self):
        self.assertEqual(self.s.db.execute("PRAGMA journal_mode").fetchone()[0],"wal")
        self.assertEqual(self.s.db.execute("PRAGMA synchronous").fetchone()[0],2)
        self.assertEqual(self.s.db.execute("PRAGMA foreign_keys").fetchone()[0],1)
    def test_18_ten_replays_one_operation(self):
        self.assertEqual(len({self.s.admit("same",request()) for _ in range(10)}),1)
        self.assertEqual(self.s.db.execute("SELECT count(*) FROM ops").fetchone()[0],1)
    def test_19_same_key_different_params_conflict(self):
        self.s.admit("same",request())
        with self.assertRaises(Conflict): self.s.admit("same",request(amount_in="20"))
    def test_20_restart_retains_idempotency(self):
        op=self.s.admit("same",request())
        self.s.close(); self.s=Store(self.p/"trade.db")
        self.assertEqual(self.s.admit("same",request()),op)
    def test_21_multiasset_reservation_atomicity(self):
        op=self.s.admit("one",request())
        self.s.balance("test-wallet",A0,"10"); self.s.balance("test-wallet",A1,"1")
        with self.assertRaises(Reject): self.s.reserve(op,{A0:"5",A1:"2"})
        self.assertEqual(self.s.db.execute("SELECT count(*) FROM reservations").fetchone()[0],0)
    def test_22_cross_strategy_budget(self):
        op=self.s.admit("one",request()); op2=self.s.admit("two",request(strategy="other"))
        self.s.balance("test-wallet",A0,"10"); self.s.reserve(op,{A0:"7"})
        with self.assertRaises(Reject): self.s.reserve(op2,{A0:"7"})
    def test_23_reservation_retry_idempotent(self):
        op=self.s.admit("one",request()); self.s.balance("test-wallet",A0,"10")
        self.s.reserve(op,{A0:"7"}); self.s.reserve(op,{A0:"7"})
        self.assertEqual(self.s.db.execute("SELECT count(*) FROM reservations").fetchone()[0],1)
    def test_24_unresolved_wallet_lane(self):
        self.step(); op=self.s.admit("two",request())
        with self.assertRaises(Reject): self.s.prepare(op,4)
    def test_25_nonce_bound_to_step(self):
        op,_=self.step()
        with self.assertRaises(Conflict): self.s.prepare(op,4)
    def test_26_no_broadcast_without_durable_artifact(self):
        _,step=self.step()
        with self.assertRaises(Reject): self.s.attempt(step)
        self.assertEqual(self.c.db.execute("SELECT count(*) FROM effects").fetchone()[0],0)
    def test_27_non_model_payload_rejected(self):
        _,step=self.step()
        with self.assertRaises(Reject): self.s.artifact(step,b"not a model artifact")
    def test_28_unknown_does_not_release_nonce(self):
        _,step=self.step(); self.s.artifact(step,PREFIX+b"x"); self.s.attempt(step)
        self.assertEqual(self.s.reconcile(step,self.c),"broadcast_unknown")
        op=self.s.admit("two",request())
        with self.assertRaises(Reject): self.s.prepare(op,4)
    def test_29_same_artifact_rebroadcast_one_model_effect(self):
        _,step=self.step(); self.s.artifact(step,PREFIX+b"x")
        for _ in range(10): self.c.send(self.s.attempt(step))
        self.assertEqual(self.c.db.execute("SELECT count(*) FROM effects").fetchone()[0],1)
        self.assertEqual(self.s.db.execute("SELECT count(*) FROM attempts").fetchone()[0],10)
    def test_30_replacement_not_implemented(self):
        _,step=self.step(); self.s.artifact(step,PREFIX+b"a")
        with self.assertRaises(Conflict): self.s.artifact(step,PREFIX+b"b")
    def test_31_halt_preserves_reconciliation(self):
        _,step=self.step(); self.s.artifact(step,PREFIX+b"x")
        self.c.send(self.s.attempt(step)); self.s.halt()
        with self.assertRaises(Reject): self.s.attempt(step)
        self.assertEqual(self.s.reconcile(step,self.c),"included")
    def test_32_database_busy_fails_closed(self):
        c=sqlite3.connect(self.p/"trade.db",isolation_level=None); c.execute("BEGIN IMMEDIATE")
        try:
            with self.assertRaises(sqlite3.OperationalError): self.s.admit("busy",request())
        finally: c.execute("ROLLBACK"); c.close()
        self.assertEqual(self.s.db.execute("SELECT count(*) FROM ops").fetchone()[0],0)
    def test_33_concurrent_idempotent_admission(self):
        def submit(_):
            s=Store(self.p/"trade.db")
            try: return s.admit("concurrent",request())
            finally: s.close()
        with ThreadPoolExecutor(max_workers=4) as pool: ids=list(pool.map(submit,range(32)))
        self.assertEqual(len(set(ids)),1)
    def test_34_backup_integrity_and_journal(self):
        op,step=self.step(); self.s.artifact(step,PREFIX+b"x")
        target=sqlite3.connect(self.p/"backup.db"); self.s.db.backup(target); target.close()
        other=Store(self.p/"backup.db")
        try:
            self.assertEqual(other.admit("one",request()),op)
            self.assertEqual(other.db.execute("SELECT count(*) FROM artifacts").fetchone()[0],1)
            self.assertEqual(other.db.execute("PRAGMA integrity_check").fetchone()[0],"ok")
        finally: other.close()

class FeedAndReorgTests(unittest.TestCase):
    def setUp(self): self.temp=tempfile.TemporaryDirectory(); self.p=Path(self.temp.name)
    def tearDown(self): self.temp.cleanup()
    def test_35_event_outbox_cursor_atomic(self):
        s=Feed(self.p/"feed.db")
        try:
            with self.assertRaises(RuntimeError): s.commit("obs",{},fault="before_outbox")
            self.assertEqual(s.db.execute("SELECT count(*) FROM events").fetchone()[0],0)
            self.assertEqual(s.db.execute("SELECT count(*) FROM outbox").fetchone()[0],0)
            self.assertEqual(s.memory,0)
        finally: s.close()
    def test_36_commit_before_memory_publication_recovers(self):
        s=Feed(self.p/"feed.db")
        with self.assertRaises(RuntimeError): s.commit("obs",{},fault="after_commit")
        self.assertEqual(s.memory,0); s.close()
        s=Feed(self.p/"feed.db")
        try: self.assertEqual(s.memory,1)
        finally: s.close()
    def test_37_observation_replay_one_outbox(self):
        s=Feed(self.p/"feed.db")
        try:
            for _ in range(10): s.commit("obs",{})
            self.assertEqual(s.db.execute("SELECT count(*) FROM outbox").fetchone()[0],1)
        finally: s.close()
    def test_38_reorg_append_only_reversal(self):
        j=Journal(self.p/"journal.db")
        try:
            j.apply("old",H1,17); self.assertEqual(j.total(),17)
            j.orphan(H1); j.orphan(H1); self.assertEqual(j.total(),0)
            j.apply("new",H2,13); self.assertEqual(j.total(),13)
            self.assertEqual(j.db.execute("SELECT count(*) FROM entries").fetchone()[0],3)
        finally: j.close()

class CrashTests(unittest.TestCase):
    def test_39_real_process_exit_model_chain(self):
        for stage in ["before_send","after_send","after_reconcile"]:
            for i in range(5):
                with self.subTest(stage=stage,iteration=i),tempfile.TemporaryDirectory() as d:
                    r=subprocess.run([sys.executable,"-m","verification.crash_worker",d,stage],
                                     cwd=ROOT,capture_output=True,text=True,timeout=10)
                    self.assertEqual(r.returncode,44,r.stderr)
                    s=Store(Path(d)/"trade.db"); c=FakeChain(Path(d)/"fake-chain.db")
                    try:
                        op=s.admit("crash",request()); step=s.prepare(op,0)
                        self.assertEqual(s.reconcile(step,c),"broadcast_unknown" if stage=="before_send" else "included")
                        c.send(s.attempt(step))
                        self.assertEqual(s.reconcile(step,c),"included")
                        self.assertEqual(c.db.execute("SELECT count(*) FROM effects").fetchone()[0],1)
                        self.assertEqual(s.db.execute("SELECT count(*) FROM ops").fetchone()[0],1)
                    finally: s.close(); c.close()
if __name__=="__main__": unittest.main(verbosity=2)
