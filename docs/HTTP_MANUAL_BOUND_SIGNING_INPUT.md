# MANUAL M3-B1 — disconnected bound signing input

Branch: `http-m3-bound-signing-input`.
Base: `7f75de8fda7f4d8d8bbfe5ff8a284b73ec667304` (M3-A scope freeze).

```ini
M1 = PASS
M2 = PASS
M3-A = SCOPE_FROZEN
M3-B1 = M3_B1_READY_FOR_REVIEW
independent_review = NOT_PERFORMED
production_broadcast = NOT_CONNECTED
controlled_canary_authorization = NOT_GRANTED
live = false
release_ready = false
```

This is the first, read-only batch of `HTTP_MANUAL_SIGNING_SCOPE.md`, not M3
signing implementation or approval. M3-B2/B3/B4 and M4/M5 remain unimplemented
by this slice. Existing migrations 001–026, M1/M2 writers/views, HTTP/config and
startup composition are unchanged. No remote operations or real-chain RPC.

## Candidate, never permission

`ManualBoundSigningInputReader.Load()` accepts authenticated identity and the
original request ID; the expected authorization/deployment scope belongs to
its disconnected operator-owned constructor. It does not accept nonce,
calldata, amount, policy hashes, quote, expiry or fee overrides from a caller.
All reads use one read-only SQLite transaction. The private helper owns no
transaction lifetime and uses SELECT only; query-only connection tests prove
both public and caller-owned transaction use without writes.

The reader first checks emergency stop/control, then verifies the immutable
MANUAL command, original 22-field M2 binding, strict completeness view and
audit; caller/scope; canonical bound time and absolute TTL; current immutable
policy/allowlists and durable C04/C05/C08 identities; active authorization
epoch/environment/build/release/deployment/wallet/chain/unit/time; complete
conserved usage; original reserved risk/usage; enabled wallet and exclusive
nonce-free bound lane. Any own attempt (even failed), foreign financial claim
or unresolved step on either the step's or operation's wallet rejects. Clock
reversal, malformed/missing/noncanonical expiry and equality reject. A final
clock check repeats TTL and authorization expiry evaluation.

The output has status `BOUND_SIGNING_INPUT_CANDIDATE_NOT_PERMISSION`, the
detached verified contract/policy/accounting identity and a pure unsigned
envelope: chain ID, from, to, value, exact data, gas limit, fee cap and tip cap.
There is deliberately no nonce, signature, raw transaction or tx hash. Original
BUY/fixed SELL/resolved percentage SELL quantities, quote, minimum, zero tip
and expiry are projected unchanged. No re-resolution, simulation or fee default
is introduced. Used budget includes this operation once, so equality to the
authorization cap does not reject a read or charge the operation again.

ExecutionAuthorized / SigningAuthorized / SendAuthorized and the contract's
permission flags are false. A WAL writer can commit stop/revoke after this
snapshot starts; an old snapshot can still return a false-permission candidate.
`TestHTTPM3B1SnapshotStopIsNotPermission` proves this boundary with a real second
SQLite handle. The next fresh read rejects. This is NOT a writer fence or a
pre-sign gate; a future separately reviewed writer must reload and fence all
mutable controls immediately before any nonce/signing effect.

## Failure, duplicate and restart behavior

Every failure returns zero output and leaves all durable tables untouched;
there is no cleanup, repair, reservation release, refund or TTL refresh.
Operational cancellation/deadline/closed-DB/query failures retain their cause
and map through the existing sanitized 503 classification. The public late
cancellation regression exercises automatic rollback before Commit and requires
both context cause and `sql.ErrTxDone`, then proves subsequent reads remain usable.
Verified policy/history rejection remains distinct from operational failure.

Repeated reads and actual file close/reopen with fresh stores preserve exact
artifact-free binding, lane, expiry and consumed budget. Sixteen concurrent
reads across two independent SQLite handles return the same candidate with
no effects. Retained 021-era address-only legacy lane pollution still rejects
after upgrade/reopen and is never repaired by the reader.

## Acceptance and limitations

The additive evidence `evidence/http-manual-bound-signing-input.json` maps all
16 permanent tests, source hashes, actual author commands/results and prior
fixture failures. Validation does not substitute for independent review.
Full ordinary tests, focused three-run B1 race, selected M1/M2/C3 and W4-C
reopen compatibility race, build/vet/staticcheck and offline reference checks
are recorded separately. No full-repository race PASS is claimed.

Structural evidence: the new reader depends only on SQLite, scope, clock,
identity parsing and existing pure verification/arithmetic helpers. There is
no signer/cipher/nonce allocator/backend/submission/broadcaster/RPC dependency
or HTTP registration. Static selectors/import checks, all-table snapshots,
query-only tests, unchanged guards and empty target economic tables establish
zero new nonce/attempt/artifact/permit/sign/send effects in valid fixtures.
These are local structural/fixture results, not production call telemetry.

Existing fixtures contain explicitly fake authorizations and, for negative
cases only, inert legacy nonce/attempt/reservation metadata. Corruption tests
disable guards (and FK for the missing-audit fixture) only in disposable DBs;
production migrations/guards are untouched. Reopen tests are not OS power-loss
tests. Offline models are not chain integration or recovery health evidence.

Application TTL remains `APPLICATION_TTL_ONLY`, `contract_deadline=false`.
No real keys/grants, signer, nonce allocation, artifact creation, RPCBackend,
SubmissionService, replay/raw send, mainnet, live, other chain, rebuild/resign/
replacement/fee bump, M3-B2/B3/B4, M4/M5/W4-D/W5 or automatic closeout.

Stop at `M3_B1_READY_FOR_REVIEW`. Next step requires stage-specific authority
for read-only independent M3-B1 review; prior M2 reviewer permission is not
reused and the author does not self-label PASS or begin B2.
