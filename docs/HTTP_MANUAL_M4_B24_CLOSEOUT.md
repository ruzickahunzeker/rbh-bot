# MANUAL M4-B2.4 formal closeout — private permit consumption only

Reviewed code head: `61acfe2b914a8077db649ed7c79060b9f260aede`.
Final hardening head: `aa15f8a98adb03e7d0b4a66934d1f88e5f4b2802`.
Reviewed main: `869bdba8d2e6d9fc83933cb423775dfaed51e906`.
PR #38 merged the exact final hardening head; the reviewed main has the same
file tree. The intervening `aa15f8a` commit changed only evidence and its
checksum. Independent code review concluded PASS at `61acfe2`; this closeout
records that conclusion and the post-merge identity check, not a new review of
changed production code.

```ini
M4-B2.3 = PASS
M4-B2.4 independent review = PASS
M4-B2.4 = PASS_ON_CLOSEOUT_BRANCH_PENDING_MERGE
controlled_checkpoint = M4_B24_PASS_ON_CLOSEOUT_BRANCH_PENDING_MERGE
M4-B2 = INCOMPLETE
M4-B = INCOMPLETE
M4 = INCOMPLETE
production_broadcast = NOT_CONNECTED
controlled_canary_authorization = NOT_GRANTED
consume = PRIVATE_CONTRACT_ONLY
send = disabled
replay = disabled
live = false
release_ready = false
```

This closeout covers only durable one-shot consumption metadata for an already
issued MANUAL `FIRST_BROADCAST` permit. Migration 043 binds the final writer to
the current MANUAL source, emergency stop, authorization, policy, artifact,
reservation, SUBMISSION lease and absolute time. Its consume witness requires
the exact transition plus audit and alert in one SQLite transaction. Direct SQL
bypass, duplicate/restart, concurrent consumers, failed audit/alert writes and
control/lease commit windows are covered by the named tests in the hardening
evidence. Migration files 001–042 were not modified.

The exact final head passed CI #186 and the PR CI #187. CI #187's `go` and
`validation-package` jobs both succeeded; the workflow includes Go tests and
race checks, vet, staticcheck, formatting and package validation. The earlier
author local combined race timeout remains recorded as timeout, not PASS.

The consumer has no production caller, signer, `SubmissionService`, raw-send,
replay or broadcaster dependency. Its zero prohibited-path counts are
structural/test evidence, not live production telemetry. No transaction send,
new nonce, economic effect, canary authorization or live operation follows from
this closeout. M4-C/D/M5 remain outside this decision.

This branch changes docs, closeout evidence and checksum only. Formal main
status becomes `M4_B24_PASS` only if its separate closeout PR is merged.
