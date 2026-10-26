# C verified execution presentation contract

The C-branch author runs below are historical, not the integrated review
receipt. The integrated local scope was independently reviewed at
`4a54c7c5785e75835a53f108056ebc0a3d42fa25`; see
`HTTP_ABC_RECOVERY_INTEGRATION.md` for its scoped conclusion and validation
attribution. No standalone C-branch or aggregate M4 closeout is implied.

Scope: HTTP presentation, OpenAPI, user documentation and tests on the shared
A/B/C integration baseline. This is not HTTP production execution, broadcaster
wiring or a new transaction state machine. Integrated code review concluded
PASS for this local recovery/history scope, not aggregate HTTP execution.
Historical branch validation snapshots below retain their original meanings.

`Record.status` and `reason_code` describe historical intake. `progress.stage`
continues to report only rejection, intake, validation and admission milestones.
The existing `execution` object preserves independent verified operation, step,
attempt, submission, receipt and current position-effect facts. No synthesized
permanent success flag or resend/replay permission is introduced.

The shared reader is owned by B and the coordinator, not presentation code.
It must use immutable request -> execution binding -> nonce claim -> signed
artifact -> first-submission binding identities. Missing unperformed stages
may be omitted. Existing downstream data with missing/conflicting provenance
must fail closed, not fall back to a latest row or serialize unverified success.
Historical reads do not depend on current authorization or phase-gated views.

Both request-ID and idempotency GETs use this contract. Duplicate POST in the
default-off history composition includes the same verified snapshot without
renewing TTL or redoing validation/admission/signing/submission. The coordinator
owns connecting that shared POST snapshot; C does not fork `Store.Get`.

## Presentation boundaries

- A signed hash is not submitted/propagated, and `submitted` is not receipt or
  canonical position success.
- `broadcast_unknown` states durable inability to prove no invocation, not a
  probability of propagation. Ambiguity stays frozen and grants no automatic
  replay/replacement or new nonce.
- `known_unsent` in this MANUAL slice means retained/frozen ownership, not retry
  or release permission.
- `observed` is not canonical. `canonical_revert` is not a successful trade.
- `orphaned`/`rolled_back` must remain visible after a prior successful receipt;
  re-canonicalization can return `canonical_success`/`active` without another
  economic action. Presentation never owns that transition.
- No raw transaction, ciphertext, encryption nonce, private key or permit is
  exposed. Capabilities stay disabled regardless of historical transaction data.

## Conservative retained-wallet boundary

This slice supports history/recovery of the retained MANUAL operation, not
wallet reuse for a second MANUAL admission/execution. A retained nonce claim
or signed artifact keeps that wallet/address outside new admission, including
after canonical success or revert. An idle lane, settled reservation or current
confirmed balance must not be presented as reusable execution allowance. Wallet
balance references remain blocked/zero for sellable and unreserved token units;
`available_native` remains null. Aliases or deleting retained provenance are
not a supported escape. See the current A/B/C recovery integration handoff's
conservative wallet-reuse boundary; reuse requires a separate accounting review.

HTTP 422 `MANUAL_WALLET_REUSE_UNSUPPORTED` identifies a new admission rejected
under this boundary. Stop/control-unavailable preflight still has highest
priority and returns 503 for admission-enabled POSTs, including duplicates.
Default-off original duplicate reads are historical, do not admit again and
remain available, as do original GET/events/recovery. No request or fee field
is changed and no new release/retry capability is added. The scoped integrated
review receipt above does not claim aggregate PASS.

## Evidence attribution

`TestHTTPCExecutionSignedHistoryAndDuplicateDoNotSubmit` uses real local signed
artifact state to check both GET routes and duplicate POST, identical public
snapshot and immutable TTL/intake history, no additional signer invocation and
no submission creation.

`TestHTTPCExecutionSubmissionOutcomesRemainHistoricalAndFrozen` uses the existing
private fake transport to create submitted, known-unsent, unknown and manual
resolution outcomes. After stop, revoke and HTTP-clock TTL expiry, both GETs and
default-off duplicate POST must retain the same verified snapshot and original
ownership (submitted keeps signed/reserved; the other three outcomes keep
frozen/frozen), preserve the database snapshot and perform no second transport
invocation. This fixture creates one local fake invocation before the checks;
it is not evidence of zero historical submissions or a real chain transaction.

`TestHTTPCExecutionReceiptAndReorgFactsAreIndependent` checks serialization
vocabulary only. It does not forge receipt rows, bypass schema guards, execute
recovery or establish canonical/reorg accounting acceptance. Actual receipt,
rollback/reapply and lifecycle event evidence belongs to the B integration and
must be tested before aggregate B/C readiness is claimed.

No remote CI, real RPC, mainnet, production HTTP execution, automatic resend,
authorization grant, live enablement or release readiness is claimed.

Initial focused author run on the C baseline:

```sh
go test ./internal/tradeapi -run '^TestHTTPCExecution' -count=1 -timeout=5m
```

Result: FAIL in 8.095s. Both GET routes and the receipt/reorg serialization
contract passed; the signed fixture and four submission-outcome fixtures each
failed at duplicate POST because the baseline handler omitted `execution` from
its historical copy. The coordinator-owned shared POST wiring is a required
integration dependency. This failed run is retained, not presented as completed
integration acceptance. Source and module cache were mounted read-only in the
existing Go 1.26.6 container, with networking disabled and module changes denied.

## Corrupt identity HTTP fixtures

`execution_identity_http_test.go` adds ten isolated corruption scenarios against
real local signed/submitted state: missing execution binding, nonce claim,
artifact command or first-submission link; an extra latest attempt/submission;
artifact-envelope hash drift; wrong exact-submission identity; and binding or
submission-row transaction-hash drift. Each scenario proves healthy GETs before
damage, then requires both GET routes to fail closed without returning an
execution snapshot, writing durable state or invoking transport again.
Principal, wallet ID and wallet-address mismatches separately require 404.

The helper explicitly drops only triggers attached to the damaged table and
temporarily disables that fixture connection's foreign-key enforcement to model
retained-store corruption. These are test-only adversarial injections, not
production permissions, proposed migrations or claims that ordinary SQL can
bypass guards.

Initial author command:

```sh
go test ./internal/tradeapi -run '^TestHTTPCExecutionIdentity' -count=1 -timeout=5m
```

Result on the unchanged C reader baseline: FAIL in 16.847s. All ten injections
were successfully constructed and exposed the baseline's healthy-looking 200
responses for corrupt history; all three foreign-identity cases passed. The
coordinator-owned immutable-identity reader is the required integration fix.
This deliberately failing baseline run is not independent acceptance of that
fix. The command used the same offline, read-only Go container setup as above.

## Lifecycle event presentation contract

The coordinator-owned event reader consumes `manual_lifecycle_events` and
reports `scope=MANUAL_ORDER_LIFECYCLE` for journal-backed MANUAL history.
`API_REJECTION_ONLY` is retained for recorded rejections. The older
`MANUAL_ORDER_INTAKE_ONLY` scope belongs to the historical C1 response and is
retained in the OpenAPI vocabulary for that history, not claimed as lifecycle
coverage by the old evidence.

The journal is append-only and request-scoped, preserving the original intake
sequence and ordering later transition events strictly after it. Lifecycle
mutation and its event witness commit together; presentation does not invent
events by merging table timestamps or own the underlying transition. Each page
contains at most 100 events, with cursor strictly greater than `after`.
`next_cursor` advances only to the last event actually returned. An empty page
returns the input cursor unchanged, so subsequent polling can discover a later
append. Cursors survive restart and are not interchangeable across requests.

This contract does not infer recovery health, enable execution/send/replay or
grant permission from a journal record. Actual append/rollback/reapply,
restart ordering, corrupt provenance and cursor-pagination acceptance must be
validated against B's integrated additive migration and source-bound writer.
The pre-integration C baseline cannot run those event acceptance tests, and no
event-test PASS is claimed by these documentation changes.

## Real recovery HTTP harness, pending B integration

`execution_recovery_http_test.go` uses the real A local first-submission fixture
and public `trade.NewRecoveryServiceWithCipher`, `ReconcileWithLease` and
`CheckReorgWithLease`. Its effect resolver is the production
`NewPonsCurveEffectResolver`, not a fake resolver. Success receipts carry
SDK-parseable Pons v2 Curve buy logs bound to the durable wallet, curve and
transaction. The only fake chain dependency provides read-only receipt/header
observations. The test-only cipher adapter delegates decryption and rejects
encryption. RECOVERY uses an independent public durable lease with wall-clock
expiry; no signer/submission/send dependency is supplied to recovery.

The intended integration checks include submitted and unknown canonical apply,
repeated reconciliation, orphan rollback and reapply; reverted receipts without
position effects; both GET routes and default-off duplicate POST; stop/revoke
plus an expired HTTP clock; and journal cursor/empty-page behavior. Original
artifact/nonce identities and exactly-once position-effect history are checked.
HTTP reads themselves must not reconcile or mutate. The setup makes exactly one
fake transport invocation before recovery; its count must stay one throughout.
That is evidence of zero additional sends, not zero historical signing or
submission in the fixture.

Author validation so far is compile-only:

```sh
go test ./internal/tradeapi -run '^$' -count=1 -timeout=5m
```

PASS in 0.026s **with no tests run**. Actual recovery execution requires B's
additive migration 045 and source-aware service/resolver plus the shared reader,
POST and event wiring. This C branch does not import or alter those files and
does not claim the new recovery tests passed, independent review or readiness.
