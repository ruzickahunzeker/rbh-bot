# HTTP API P9 — controlled canary preflight scope freeze

Status: `P9_SCOPE_FROZEN`

P9 defines a final preflight package for a future controlled canary. It is a
read-only readiness and evidence gate. It does not create, arm, or grant a
runtime authorization, connect a production broadcaster, or send a chain
transaction.

## Required preflight identity

The package must bind one immutable deployment to:

```text
build_sha / release_id / deployment_id
environment
chain profile = Robinhood/4663
protocol = Pons v2 Curve
wallet allowlist
contract/token allowlists and runtime hashes
policy version/hash
C04/C05/C07/C08 attestation identities
```

Any mismatch, missing source, stale epoch, expired policy, or unavailable RPC
observation fails preflight closed. The package records the exact source and
hashes used for review; it does not infer permission from readiness.

## Budget and operating limits

The preflight must display the proposed durable limits without consuming them:

```text
max_operations
max_total_input
per-wallet active reservation cap
minimum retained balance
maximum TTL
maximum fee/gwei policy
```

The limits must match the immutable authorization candidate and C07 risk policy.
No HTTP request can raise, replace, or refresh these values.

## Readiness and emergency controls

The report keeps the three readiness dimensions separate:

```text
canary_admission_ready
submission_send_ready
recovery_ready
```

Emergency stop is checked first and any read failure means stopped. Stop,
authorization revoke/expiry, deployment drift, lease loss, RPC disagreement,
or TTL expiry block new economic work. Existing submitted or
`broadcast_unknown` work remains queryable and recoverable; frozen lanes and
reservations are not released automatically.

## Evidence package

P9 preflight must include:

- source/config file identity and deployment hash;
- durable C04/C05/C07/C08 attestations;
- allowlist and runtime identity hashes;
- budget and reservation conservation evidence;
- emergency-stop and revoke/expiry checks;
- duplicate/restart and shutdown/drain checks;
- readiness and alert/metric snapshots;
- prohibited-path scan showing zero signer/send/mainnet reachability;
- operator approval metadata as a separate, explicit artifact.

The operator approval artifact is intentionally absent from this scope. Its
presence is required for any later authorization grant and must be reviewed
separately.

## Status ceiling

```ini
P8 = PASS
P9 = READY_FOR_REVIEW
preflight = NOT_AUTHORIZED
controlled_canary_authorization = NOT_GRANTED
production_broadcast = NOT_CONNECTED
canary_admission_ready = false
submission_send_ready = false
recovery_ready = false
live = false
release_ready = false
```

P9 stops at independent preflight review. No authorization is granted by this
scope freeze.
