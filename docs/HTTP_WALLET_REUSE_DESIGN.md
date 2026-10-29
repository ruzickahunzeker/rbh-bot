# Retained MANUAL wallet reuse — design decision, not implementation

This is the separate design work alongside the accepted local first-order POST
harness. Current code still rejects a second admission for a wallet ID OR
normalized address with a retained MANUAL nonce claim. No schema, financial
ledger, gate, finality assumption, claim deletion or automatic repair is changed.

## Why settlement alone cannot authorize reuse

| Old outcome | Current ownership | Why another admission remains unsupported |
| --- | --- | --- |
| Signed/pending submitted | Original signed lane and reserved risk | First outcome unresolved |
| broadcast_unknown/manual resolution | Frozen lane/risk | Lack of receipt/expiry cannot prove non-propagation |
| known_unsent retained | Original signed nonce retained/frozen | No reviewed release or resume contract exists |
| Canonical success | Committed risk, consumed lifetime usage | A later orphan must still roll back and refreeze |
| Canonical revert | Released risk, consumed lifetime usage | Old signed/receipt history can still change under reorg |
| Orphaned receipt | Frozen risk/usage/lane | Re-canonicalization must use the original generation |

The reviewed failure is concrete: after old canonical settlement, a new active
reservation can occupy C07's unique active wallet slot; the old reorg then fails
to refreeze its reservation. Removing the unique constraint, hiding the old
operation or automatically canceling the newer order does not preserve the
current invariants. Lifetime authorization count/input are cumulative admission
usage, not spendable credit to refund after success/revert.

## Contracts a future model must resolve together

1. Which durable owner serializes wallet lane and account nonce across retained
   historical generations, without conflating execution scheduling with risk?
2. How does old reorg exposure coexist with the new operation's reserved risk,
   atomically, using one authoritative unit-aware model rather than another
   financial ledger? Preserve native gas/outlay versus token input units.
3. Which actual chain observation, if any, makes a generation permanently
   releasable? Confirmation count or elapsed TTL alone is not an accepted
   irreversible-finality proof. Any bounded-finality trust assumption needs
   explicit acceptance plus a fail-closed contradiction procedure.
4. What happens when a later operation has already propagated before the old
   one is orphaned? Never rewrite nonce/artifact identity, reverse a real send,
   auto-cancel another economic action or silently accept overcommit.
5. How do restart, authorization budgets, aliases, direct SQL, historical
   imports, audit/alert and cursor witnesses conserve the chosen model?

## Viable planning directions, not approved alternatives

- Keep the present retained-generation exclusion until an explicit reviewed
  release condition exists. Safe but deliberately not repeat-trading delivery.
- Investigate an explicit durable finality/retirement contract. This would
  narrow old-reorg guarantees and requires accepted chain trust assumptions;
  it is not a small cleanup or an expiry-based release.
- Investigate one conserved wallet ownership/exposure model able to retain old
  reorg liability while admitting a new economic operation. If that requires
  reinterpreting existing C07 operation-scoped uniqueness or limits, stop for a
  separately accepted invariant change; it cannot be smuggled into this slice.

No direction has been selected or authorized for implementation here. The local
first-order harness can be validated independently while this decision remains
open, but must not be marketed as repeated trading on one configured wallet.
Production transport/custody and actual execution authorization are separate
decisions even after wallet reuse is solved.

## Required discriminating acceptance before reuse implementation approval

Construct old canonical/revert -> genuine second admission -> old reorg while
the new order is reserved, signed, submitted and unknown. For each window prove
conserved typed exposure, no unique-conflict blocking old rollback, no automatic
second action/cancellation, immutable nonce/artifact, unchanged lifetime usage,
and recovery availability after stop/revoke/restart. Include two-handle races,
SQL bypasses, imported conflicting history, address aliases and fault rollback.
Do not substitute two independent per-wallet first-order fixtures for this
multi-generation acceptance. Existing rejection remains until that model and
its evidence are independently reviewed.
