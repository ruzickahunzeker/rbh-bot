# C07 Controlled Canary Admission

This Slice adds only the durable admission and risk-control layer for a future controlled Pons v2
Curve canary. It does not wire production submission, enable live mode or contact mainnet.

Admission is fail-closed and requires durable C04/C05/C08 attestations, `CONTROLLED_CANARY` mode,
an inactive emergency stop, exact policy-version binding, execution-wallet and Pons v2 Curve
contract/runtime/token allowlists, application TTL validity and every risk limit. All accepted
usage is reserved atomically with the decision and audit event. Arbitrary-precision decimal text
is accounted in Go; SQLite integer coercion is not used for economic values.

The only schema modes are `DISABLED` and `CONTROLLED_CANARY`. Emergency stop has highest priority.
An absent or unreadable control row is a rejection. The implementation contains no signer,
submission-service, broadcast or mainnet dependency.

Implementation completion is `READY_FOR_REVIEW`, not PASS. Live and release readiness remain
false, and a separate independent review and closeout are required.

## Evidence hardening

The independent review blockers are addressed by an additive hardening migration and tests:
emergency stop is evaluated first; C04/C05/C08 attestations are bound to the landed evidence
hashes; policy, allowlist and durable operation-source records are immutable; request values must
match a hashed durable source; gas cost is derived from gas limit and fee cap; and a schema-level
unique index enforces one active reservation per wallet.

Every request attempt, including dedupe, is durable and feeds the admission metrics. A mixed
10,000-request workload proves one accepted operation, 4,999 dedupes, 5,000 wallet-lane
rejections, one active reservation and exact request conservation. Fault tests after usage,
reservation and decision writes and immediately before commit prove all-or-nothing rollback.

Hardening restores only `READY_FOR_REVIEW`; it does not close C07 or authorize live execution.
