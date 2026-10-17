# MANUAL M4-B2.3 — private FIRST_BROADCAST permit issuance

Branch: `http-m4b23-first-permit`.
Base: `2a238fc9a1bb7547153fda43086e8724b6b1fb75` (accepted B23 design).

```ini
M1 = PASS
M2 = PASS
M3 = PASS
M4-A = PASS
M4-B1 = PASS
M4-B2.1 = PASS
M4-B2.2 = PASS
M4-B2.3 scope = ACCEPTED
M4-B2.3 = READY_FOR_REVIEW
checkpoint = M4_B23_READY_FOR_REVIEW
independent_M4_B23_review = NOT_PERFORMED
M4-B2 = INCOMPLETE_NOT_READY_NOT_PASS
M4-B = INCOMPLETE_NOT_READY_NOT_PASS
M4 = INCOMPLETE_NOT_PASS
production_broadcast = NOT_CONNECTED
controlled_canary_authorization = NOT_GRANTED
live = false
release_ready = false
```

## Implemented boundary

Additive migration040 adds immutable `manual_first_broadcast_issuances` metadata
and exact audit/alert. It does not create a second economic state machine. The
private `manualFirstBroadcastIssuer` runs fresh B22 preparation, read-only
observation and transaction-local finalCheck on its own `BEGIN IMMEDIATE`
writer, then binds one exact FIRST_BROADCAST snapshot and one ISSUED permit.
Permit expiry is absolute and computed once as the minimum of original operation
expiry, authorization expiry, SUBMISSION lease expiry and the immutable local
verification window. No restart/duplicate refresh, relative timer or new TTL.

The issuer consumes no permit, sends no transaction, invokes no signer,
SubmissionService, replay path, broadcaster or nonce allocator. It returns only
private permit metadata. UNKNOWN_REPLAY, permit consumption, immediate-pre-send,
submission/outcome and recovery remain outside this slice. No HTTP/config/startup
caller or real authorization grant was added.

Existing MANUAL deny domains stay closed except the exact permit/snapshot
exception for a complete issuance row. Migration040 replaces only the two
MANUAL INSERT deny definitions with same-named exact exceptions; migrations001–039
remain byte-identical. The exception still requires immutable issuance/source
identity. A B2.3-specific consume trigger rejects state mutation for issued
MANUAL permits. Existing non-MANUAL canary behavior is unchanged.

## Durable identity and atomicity

Issuance binds operation/attempt/authorization ID+epoch, original source JSON
and hash, process hash, runtime observation digest, clock generation, exact raw
artifact hash, tx hash, issued_at/expires_at, and state=ISSUED. The same writer
inserts clock event, issuance row, FIRST_BROADCAST PASS snapshot and send permit;
audit/alert triggers are part of the transaction. Any audit/alert/permit failure
rolls back the whole metadata set. Issuance is immutable and one per operation /
attempt. Exact duplicates rerun fresh verification and return existing identity
without another clock event, audit, snapshot, permit or expiry window. Changed
artifact or binding is a conflict.

No persisted row is treated as proof that external time/process/RPC/crypto ran;
the consumer must later fresh-check independently. B22's trusted composition,
Linux running-image identity, source/artifact authentication and application TTL
semantics remain unchanged. `contract_deadline=false` remains unchanged.

## Tests and author validation

Focused tests cover migration039->040 upgrade/idempotence, artifact mismatch,
fresh metadata-only issuance, duplicate no-refresh, audit failure rollback and
consume/replay disabled. Controlled fixtures assert no transaction submission and
no new signing state. Full ordinary repository regression passed under approved
loopback/Unix-socket fixture permission; selected B23 race passed. Build/vet/
staticcheck/gofmt/diff and package checks are recorded in additive evidence.

The initial full ordinary run without fixture permission failed only on sandbox
network/socket restrictions and was retained as environment evidence. No real
chain, key, authorization, production transport or live action was used.

Maximum state is `M4_B23_READY_FOR_REVIEW`; independent review has not run and
this implementation must not be marked PASS or closed out by the author.
