# HTTP API P8 formal closeout

Status: `P8_PASS`

Reviewed implementation head: `d4f78af`
Independent review receipt: `411dcb4`
Closeout branch: `http-p8-canary-authorization-closeout`

P8 controlled canary authorization scope is formally closed. The closeout
covers durable deployment/epoch identity, state-based revoke/expiry
invalidation, higher global epoch for replacement authorizations, C04/C05/C07/
C08 binding, chain/wallet/protocol/contract/token allowlists, atomic budgets,
emergency-stop priority, readiness separation, duplicate/restart behavior, and
frozen `broadcast_unknown` recovery.

This closeout does not grant a canary authorization. No authorization record is
armed, no production broadcaster is connected, and no chain transaction is
sent. BSC/Flap, Base, Pons v4, Long, LP, Solana, unrestricted live, and P9 are
outside this closeout.

Formal state:

```ini
P0_P7 = PASS
P8 = PASS
P9 = NOT_STARTED
controlled_canary_authorization = NOT_GRANTED
production_broadcast = NOT_CONNECTED
canary_admission_ready = false
submission_send_ready = false
recovery_ready = false
live = false
release_ready = false
```

Any future authorization grant requires a separate operator decision after a
new scope review; it is never inferred from this closeout.
