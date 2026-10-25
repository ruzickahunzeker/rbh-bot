# MANUAL M4-B2.4 — private FIRST_BROADCAST permit consumption contract

This slice is the next step after M4-B2.3. It is limited to the durable,
one-shot consumption contract for an already-issued MANUAL `FIRST_BROADCAST`
permit. It does not authorize a broadcaster, a signer, a replay path, or live
execution.

```ini
M4-B2.3 = PASS
M4-B2.4 scope = ACCEPTED_FOR_IMPLEMENTATION
M4-B2.4 implementation = LANDED_PR_38
M4-B2.4 independent review = PASS_ON_REVIEWED_CODE_HEAD
M4-B2.4 formal closeout = COMPLETE_PR_39
M4-B2.4 = PASS
production_broadcast = NOT_CONNECTED
controlled_canary_authorization = NOT_GRANTED
consume = PRIVATE_CONTRACT_ONLY
send = DISABLED
replay = DISABLED
live = false
release_ready = false
```

PR #38 landed migration 043 and hardening on
`main@869bdba8d2e6d9fc83933cb423775dfaed51e906`; 001–042 remain unchanged.
The independent code review of `61acfe2` concluded PASS. Final head `aa15f8a`
only added CI evidence and checksum; its tree exactly matches merged main.
The separate formal closeout landed via PR #39 and is recorded in
`HTTP_MANUAL_M4_B24_CLOSEOUT.md`.

## Frozen boundary

The implementation may consume only the exact MANUAL `FIRST_BROADCAST` permit
issued by B2.3. Consumption must be one-shot, durable, auditable and bound to
the original operation, attempt, authorization id/epoch, policy/deployment
identity, artifact hash and transaction hash. A duplicate, restart or stale
caller must not consume it again.

The consume transaction must revalidate the immutable artifact and all mutable
controls (emergency stop, authorization state/epoch, deployment identity,
reservation state, policy and absolute TTL) in the same writer transaction as
the state transition. Any failed audit/alert write rolls the transition back.

`FIRST_BROADCAST` and `UNKNOWN_REPLAY` remain separate purposes. This slice
does not issue or consume replay permits, query a transaction, allocate a
nonce, create a submission, mark a send intent, call a signer, call
`SubmissionService`, call `SendRawTransaction`, or invoke a production
broadcaster. No raw bytes leave the process.

An expired, revoked, stopped, drifted or otherwise invalid permit remains
unconsumed and is recorded as a rejection. Consumption never rebuilds,
re-signs, replaces, fee-bumps or changes the nonce/artifact.

## Required evidence

- exact B2.3 permit consumes once and only once;
- duplicate and restart attempts consume zero additional permits;
- artifact, operation, attempt, wallet, authorization epoch and policy
  mismatches reject without mutation;
- stop, revoke, expiry, reservation freeze/release and deployment drift reject
  in the authoritative transaction;
- forced audit/alert failure rolls back the consume;
- concurrent consumers produce exactly one `CONSUMED` row;
- `UNKNOWN_REPLAY` remains unavailable and all prohibited dependency counters
  remain zero;
- no `transaction_submissions`, send-intent, nonce or economic-effect row is
  created by this slice.

The hardening checkpoint was `M4_B24_READY_FOR_REVIEW`; PR #39 merged the
separate docs/evidence closeout, establishing `M4_B24_PASS` on main.
