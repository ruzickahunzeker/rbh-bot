# Controlled canary activation request template

Status: `NOT_AUTHORIZED`

This file is a review template only. It contains no authorization grant,
operator approval, private credential, broadcaster endpoint, or live setting.

## Required operator fields

```yaml
decision: NOT_AUTHORIZED
environment: ""
build_sha: ""
release_id: ""
deployment_id: ""
chain: robinhood
chain_id: 4663
protocol: pons_v2_curve
wallet_allowlist: []
contract_allowlist: []
token_allowlist: []
policy_version: ""
policy_hash: ""
max_operations: 0
max_total_input: ""
expires_at: ""
emergency_stop_owner: ""
rollback_owner: ""
monitoring_owner: ""
}
```

## Required review before any grant

- build/release/deployment identity matches the reviewed artifact;
- C04/C05/C07/C08 attestations are current and durable;
- wallet, contract, token, protocol, and policy hashes match the intended
  controlled scope;
- max operations and total input are within approved limits;
- expiry is absolute and finite;
- emergency stop, monitoring, rollback, and recovery owners are assigned;
- `canary_admission_ready`, `submission_send_ready`, and `recovery_ready` are
  independently true;
- production broadcaster and RPC endpoint authentication are explicitly
  reviewed;
- no Pons v4, Long, LP, BSC/Flap, Base, Solana, or strategy expansion is
  included.

Until an explicit operator decision is recorded and independently reviewed,
the runtime remains:

```ini
controlled_canary_authorization = NOT_GRANTED
production_broadcast = NOT_CONNECTED
live = false
release_ready = false
```
