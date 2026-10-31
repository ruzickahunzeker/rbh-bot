# HTTP API P8 — controlled canary authorization scope freeze

Status: `P8_SCOPE_FROZEN`

P8 defines the authorization boundary for a future controlled canary. It does
not grant authorization, connect a production broadcaster, send a transaction,
or enable live. The only allowed canary mode is `CONTROLLED_CANARY`; all other
runtime modes remain `DISABLED`.

## Authorization material

An authorization record is durable, immutable, and bound to:

```text
authorization_id
authorization_epoch
environment
build_sha / release_id / deployment_id
chain profile
protocol and contract/token allowlists
wallet allowlist
policy version and policy hash
max_operations
max_total_input
absolute expires_at
C04/C05/C07/C08 gate attestations
```

Deployment identity changes invalidate the prior authorization. Restart does
not reset usage. Duplicate requests do not consume budget twice. Revocation or
expiry increments the authorization epoch and fails all new admission,
signing, and submission gates closed.

Authorization is never inferred from HTTP configuration, API credentials,
readiness, a historical admission decision, or a local permit. It requires an
explicit durable operator action outside the HTTP order path.

## Readiness and controls

The canary requires all three readiness dimensions independently:

```text
canary_admission_ready
submission_send_ready
recovery_ready
```

Emergency stop has highest priority. Its read failure defaults to stopped and
blocks new economic actions. It does not stop query, receipt, canonical, or
reorg recovery for existing propagated or ambiguous transactions.

The authorization budget is atomic with admission reservation and usage. A
budget overcommit, unexplained accept, stale attestation, source drift,
deployment drift, or contradictory runtime observation rejects the operation
and records durable audit/alert evidence.

## Canary allowlist

The first canary may target only one explicitly listed wallet, one chain
profile, one protocol adapter, and listed token/contract identities. The
current planned profile is Pons v2 Curve on Robinhood/4663. BSC/Flap, Base,
Pons v4, Long, LP, Solana, strategy expansion, and unrestricted live are out of
scope.

## Stop, rollback, and recovery

Emergency stop, authorization revoke, policy drift, deployment drift, lease
loss, RPC disagreement, or readiness loss stop new admission/sign/send. They
must not release a `broadcast_unknown` lane or reservation. Recovery continues
query-first and reconciles receipt/canonical/reorg state.

Rollback means revoke authorization, advance epoch, drain submission work, and
retain durable evidence. It never rebuilds, resigns, replaces, fee-bumps, or
allocates a new nonce.

## Acceptance evidence

Before any authorization grant, P8 must prove:

- immutable deployment identity and epoch invalidation;
- wallet/chain/protocol/contract/token/policy binding;
- atomic max-operations and total-input budget conservation;
- emergency-stop highest-priority behavior and fail-closed read failures;
- three readiness dimensions and revoke/expiry behavior;
- duplicate/restart idempotency;
- `broadcast_unknown` remains frozen through stop, TTL, revoke, and shutdown;
- recovery remains available while new economic action is blocked;
- zero prohibited signer/send/mainnet paths in the reviewed composition;
- controlled harness evidence with no real chain transaction.

## Status ceiling

```ini
P7 = PASS
P8 = READY_FOR_REVIEW
controlled_canary_authorization = NOT_GRANTED
production_broadcast = NOT_CONNECTED
canary_admission_ready = false
submission_send_ready = false
recovery_ready = false
live = false
release_ready = false
```

Implementation and review must stop before any real authorization grant. A
separate operator decision is required after P8 independent review and
closeout.
