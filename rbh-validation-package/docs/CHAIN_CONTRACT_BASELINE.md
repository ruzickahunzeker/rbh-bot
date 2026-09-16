# C02｜Chain / Contract Baseline

Status: **C02-PONS-CURVE PASS / C02-PONS-V4 PARTIAL / C02-LONG NOT STARTED**.
The original combined capture remains `CAPTURED_REQUIRES_CLASSIFICATION_REVIEW`; that status still
applies to execution contracts, but no longer blocks the Curve-only Feed slice.

## Pons Curve Feed classification

`evidence/c02-pons-curve-classification.json` classifies the Feed identity path at block 64478214:

- Pons factory: `direct_implementation`, trusted. Its runtime hash matches the earlier block
  64376127 capture.
- Fixture bonding curve: `direct_implementation`, trusted. Its identity is anchored by the
  factory `TokenLaunched` log and its runtime hash is the canonical Curve Feed allowlist value.
- LaunchAndBuy: `direct_implementation`; recorded for targeted-transaction discovery but not
  required as a successful-event emitter.

For each address the classifier checks ERC-1967 implementation and beacon slots, EIP-1167 runtime,
then executable bytecode opcodes. Solidity CBOR metadata is excluded from opcode review so random
metadata bytes cannot create a false `DELEGATECALL`. The authoritative source is locked to official
`ponsdotdev/ponsfamily` commit `58cba1cb2bbc7f8dc4b13b810929461eb36bc15b` and parser-sdk
AddressBook commit `72cf9cee394bbd2566fca1d03202b05981eb51b4`.

The production-consumable allowlist is `configs/mainnet/pons-curve-feed.lock.json`. Any future
factory-emitted curve whose runtime hash differs from the canonical curve hash must remain
untrusted until separately classified.

The read-only capture at block `64376127` (`0x7607f685fe685f7952bd366fb338ee32c26bd35f517a11065a725ebb9ebaefad`)
confirmed chain ID 4663 and non-empty runtime code for every Pons V0.1 and shared Uniswap v4
address selected from the locked Trade SDK. Exact addresses, Keccak-256 runtime hashes and code
sizes are in `evidence/chain-baseline.json` and the production-consumable copy is
`configs/mainnet/contracts.lock.json` in the repository root.

The capture also reads the ERC-1967 implementation slot at the same block. WETH and USDG had
non-zero implementation addresses with non-empty implementation code. Other entries are labeled
`no_eip1967_implementation_slot`; this does **not** prove that every one is non-proxy, because
beacon, diamond, minimal-clone and custom proxy patterns require separate classification.

## Reproduction

Use a credential-free or secret-injected read-only RPC endpoint:

```bash
ROBINHOOD_RPC_URL=... go run ./cmd/chain-baseline \
  -block 64376127 \
  -output configs/mainnet/contracts.lock.json
```

The command permits only chain/header, code and storage reads through `ethclient`; it has no key,
signing or send path. The endpoint itself is redacted from evidence.

The generator fails closed if the locked Parser and Trade SDK shared AddressBooks disagree.
`TestLockedSDKAddressBooksMatch` runs the same comparison in CI without requiring RPC access;
the baseline contract list also has an offline uniqueness/non-zero-address test.

## Remaining C02 gates

- Independently classify every `no_eip1967_implementation_slot` entry.
- Verify beacon/custom proxy slots where applicable.
- Record authoritative deployment/source evidence, not only an RPC observation.
- Re-run the locked-block capture from an independent archive RPC.

Until these gates are reviewed, `status` remains `CAPTURED_REQUIRES_CLASSIFICATION_REVIEW` and
execution must fail closed.
