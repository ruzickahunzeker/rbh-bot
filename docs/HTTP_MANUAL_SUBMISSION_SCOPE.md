# MANUAL M4 — controlled local submission/recovery scope

Frozen baseline: local M3 closeout
`1b6b528fea77b57f94f8bd1b964a425e17192ff5`, not a claim about remote main.
The user accepted a read-only scope freeze, then authorized continuation of
M4-A only. This document records that boundary; it is not production authority.

```ini
M1 = PASS
M2 = PASS
M3 = PASS
M4 = CONTROLLED_LOCAL_SUBMISSION_AND_RECOVERY_ADAPTATION
M4 scope = FROZEN
M4-A = READY_FOR_REVIEW
M4-B = NOT_STARTED
M4-C = NOT_STARTED
M4-D = NOT_STARTED
M5 = NOT_STARTED
production_broadcast = NOT_CONNECTED
controlled_canary_authorization = NOT_GRANTED
live = false
release_ready = false
```

Scope stays Robinhood 4663 / Pons v2 Curve / native quote, BUY/fixed SELL/
already-resolved percentage SELL. APPLICATION_TTL_ONLY, contract_deadline=false.
No BSC/Flap/Base/Solana, Long/LP/strategy expansion or unrestricted live.

## Inspected compatibility gaps

- MANUAL uses versioned full-Facts `manual:v1` AAD; legacy submission/recovery
  uses `artifactAAD` without that suffix. Format must come from immutable durable
  ownership; never try legacy AAD after MANUAL authentication fails.
- Existing canary gates use `canary_admission_sources`; MANUAL owns its original
  command, typed risk/usage and binding. Do not forge a legacy source or reserve
  another execution budget to satisfy those joins.
- 036 verifies only signed/prebroadcast, explicitly excluding submission,
  receipt and position rows. New downstream phase completeness is required
  later; old initial-only views cannot become permissive catch-all readers.
- SubmissionService currently retains an ExecutionKernel containing a signer.
  Future submission adaptation must depend on exact artifact reading/verification
  and transport only, not gain signing/rebuild capability by reusing that kernel.
- Legacy effect resolution reads dry-run results; future MANUAL receipt effects
  must be derived and validated against the original durable MANUAL contract.
- no-arm/no-permit/no-PASS and downstream lifecycle guards intentionally remain
  closed. Any future exact exception requires an additive reviewed contract,
  not configuration bypass or blanket trigger removal.

## Frozen ownership, gates and lifecycle

Reuse Operation -> ExecutionStep -> TransactionAttempt and existing submission/
receipt/effect history. New overlays may bind identities/gates/permits/audit,
never become a second economic state machine, financial reservation or ledger.
Original request/amount/minimum/calldata/fee including zero tip, nonce, policy/
quote block/hash, absolute expiry, auth epoch/deployment and consumed lifetime
usage remain exact; no re-sign, rebuild, replacement, fee bump, new nonce/TTL/
epoch, requote, percent re-resolution or duplicate budget consumption/refund.

Future local submission flow:

```text
original MANUAL signed artifact
  -> source-aware exact verification
  -> fresh authoritative submission gates
  -> FIRST_BROADCAST one-shot permit / independent UNKNOWN_REPLAY permit
  -> durable transaction_submissions and send-intent ambiguity boundary
  -> controlled fake transport only
  -> durable outcome
  -> query-only receipt/canonical/reorg reconciliation
```

Admission/pre-sign remain independent earlier stages; old PASS metadata never
substitutes for current send permission. Future immediate-pre-send rechecks
stop first, active auth/epoch/deployment, gate/policy/allowlists, TTL, exact
artifact, original reservation/usage and SUBMISSION lease in the consume/send
writer boundary. Re-read trustworthy current time after RPC; reject equality,
noncanonical time, reversal, missing controls or unverifiable/contradictory
runtime identity. Permit expiry cannot exceed original TTL/auth expiry.

Keep W4-B `send_intent_at` semantics unchanged: durable ambiguity boundary,
not proof of invocation/broadcast. Marker committed then crash before RPC may
conservatively become broadcast_unknown. known_unsent needs durable proof no
invocation, or explicitly trusted and durably recorded deterministic rejection.
Stop/TTL/auth expiry/shutdown never releases ambiguous ownership or initiates a
second action. Unknown replay is never automatic: query tx/receipt/nonce first,
fresh purpose-specific one-shot permit and exact same raw bytes only.

Recovery has no signer/broadcaster/permit/nonce allocator and remains independent
of admission/send readiness. Existing propagated/ambiguous work remains query/
receipt/canonical/reorg eligible after stop/auth/TTL/deployment changes. Apply,
rollback and reapply must be exactly once with original typed accounting; do
not overwrite a later operation's wallet lane. SUBMISSION/RECOVERY leases remain
independent and fence mutations in their own transaction.

## Ordered slices and authorization

| Slice | Scope | Maximum checkpoint |
|---|---|---|
| M4-A | Private SELECT-only source-aware artifact metadata contract, exact MANUAL format/AAD validation and cross-phase reader tests; no new phase/schema/send | M4_A_READY_FOR_REVIEW |
| M4-B | Additive exact submission/outcome/gate/permit contract bound to original typed accounting, retaining old deny domains | M4_B_READY_FOR_REVIEW |
| M4-C | Private local coordinator with fake transport, query-first replay and query-only recovery | M4_C_READY_FOR_REVIEW |
| M4-D | Cumulative crash/restart/concurrency/compatibility/static dependency evidence | M4_READY_FOR_REVIEW |
| Independent review/closeout | Separate review, then docs/evidence only after PASS | M4_PASS only after closeout |

M4-A must be independently reviewed before opening later slices. Later migration
numbers start at additive 037; 001–036 remain byte-identical. Precise direct-SQL
guards must bind full OLD/NEW/rowid/owner identities and atomic audit/alert, deny
IGNORE/REPLACE/alias/adoption/partial-history bypass. no-arm stays closed. Future
permit/PASS exceptions need explicit scope/review; no runtime test_mode bypass.

Acceptance includes three-side exact artifact/AAD isolation; expired/denied
transport0; one owner/permit under duplicate/restart/takeover; marker/RPC/COMMIT
crash matrix; query-first exact replay; stop/expiry recovery continuity; exactly-
once canonical/reorg accounting and zero production reachability. Fake local
transport/signing fixture counts are distinct from zero production counts.

No HTTP parameter or send/replay endpoint change, production composition in
internal/app/run.go, real key/RPC/grant/mainnet, production signer/broadcaster,
live/release change, M5/W4-D/W5 or remote GitHub operation is authorized. Current
permission is only M4-A implementation, not the entire table or real execution.
