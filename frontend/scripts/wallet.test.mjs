import test from "node:test";
import assert from "node:assert/strict";
import { hexToBytes, bytesToHex } from "@noble/hashes/utils";
import { checksumAddress, deriveKeys, isEvmAddress, isMnemonic, isSolanaAddress, newMnemonic, slip10Ed25519 } from "../lib/wallet/keys.ts";
import { open, seal, WrongPasswordError } from "../lib/wallet/keystore.ts";

const ABANDON = "abandon abandon abandon abandon abandon abandon abandon abandon abandon abandon abandon about";

test("SLIP-0010 ed25519 test vector 1", () => {
  const seed = hexToBytes("000102030405060708090a0b0c0d0e0f");
  assert.equal(bytesToHex(slip10Ed25519(seed, "m")), "2b4be7f19ee27bbf30c667b642d5f4aa69fd169872f8fc3059c08ebae2eb19e7");
  assert.equal(bytesToHex(slip10Ed25519(seed, "m/0'")), "68e0fe46dfb67e368c75379acec591dad19df3cde26e63b93a8e704f1dade7a3");
  assert.throws(() => slip10Ed25519(seed, "m/0"), /hardened/);
});

test("the well-known phrase gives the well-known EVM address", () => {
  const keys = deriveKeys(ABANDON);
  assert.equal(keys.evm.address, "0x9858EfFD232B4033E47d90003D41EC34EcaEda94");
  assert.ok(isSolanaAddress(keys.solana.address));
  // Case and spacing don't change the keys.
  assert.equal(deriveKeys(`  ${ABANDON.toUpperCase()}  `).solana.address, keys.solana.address);
});

test("phrases and addresses are checked", () => {
  const phrase = newMnemonic();
  assert.equal(phrase.split(" ").length, 12);
  assert.ok(isMnemonic(phrase));
  assert.ok(!isMnemonic("abandon abandon abandon"));
  assert.equal(checksumAddress("0x5aaeb6053f3e94c9b9a09f33669435e7ef1beaed"), "0x5aAeb6053F3E94C9b9A09f33669435E7Ef1BeAed");
  assert.ok(isEvmAddress("0x5aAeb6053F3E94C9b9A09f33669435E7Ef1BeAed"));
  assert.ok(!isEvmAddress("0x5aAeb6053F3E94C9b9A09f33669435E7Ef1BeAeD"));
  assert.ok(!isSolanaAddress("not-base58-0OIl"));
});

test("a sealed phrase opens with its password only, and refuses an edited keystore", async () => {
  const ks = await seal(ABANDON, "correct horse battery");
  assert.equal(ks.addresses.evm, "0x9858EfFD232B4033E47d90003D41EC34EcaEda94");
  assert.ok(!JSON.stringify(ks).includes("abandon"));
  assert.equal(await open(ks, "correct horse battery"), ABANDON);
  await assert.rejects(open(ks, "wrong password!!"), WrongPasswordError);
  // Someone swaps the address the keystore names: the authenticated data no longer matches.
  await assert.rejects(open({ ...ks, addresses: { ...ks.addresses, evm: "0x0000000000000000000000000000000000000001" } }, "correct horse battery"), WrongPasswordError);
  await assert.rejects(seal(ABANDON, "short"), /at least/);
});

import { secp256k1 } from "@noble/curves/secp256k1";
import { ed25519 } from "@noble/curves/ed25519";
import { keccak_256 } from "@noble/hashes/sha3";
import { concatBytes, utf8ToBytes } from "@noble/hashes/utils";
import { base58, base64 } from "@scure/base";
import { rlp, intBytes, erc20Transfer, signEip1559, signingHash, usdcTransferTx } from "../lib/wallet/evm-tx.ts";
import { associatedTokenAddress, signSolana, tokenTransfer, solTransfer } from "../lib/wallet/solana-tx.ts";
import { ownershipMessage, personalSign, proveOwnership } from "../lib/wallet/proof.ts";
import { fromMinor, toMinor } from "../lib/wallet/chains.ts";
import { evmAddressOf } from "../lib/wallet/keys.ts";

const hex = (b) => bytesToHex(b);

test("RLP matches the spec's examples", () => {
  const s = (x) => utf8ToBytes(x);
  assert.equal(hex(rlp(s("dog"))), "83646f67");
  assert.equal(hex(rlp([s("cat"), s("dog")])), "c88363617483646f67");
  assert.equal(hex(rlp(new Uint8Array())), "80");
  assert.equal(hex(rlp([])), "c0");
  assert.equal(hex(rlp(intBytes(0n))), "80");
  assert.equal(hex(rlp(intBytes(15n))), "0f");
  assert.equal(hex(rlp(intBytes(1024n))), "820400");
  assert.equal(hex(rlp(s("Lorem ipsum dolor sit amet, consectetur adipisicing elit"))).slice(0, 4), "b838");
});

test("an EIP-1559 USDC transfer is signed by the vault's EVM key", () => {
  const keys = deriveKeys(ABANDON);
  const tx = usdcTransferTx({ chainId: 84532, usdc: "0x036CbD53842c5426634e7929541eC2318f3dCF7e", to: "0x5aAeb6053F3E94C9b9A09f33669435E7Ef1BeAed", amount: 1_500_000n, nonce: 3n, maxPriorityFeePerGas: 1_000_000n, maxFeePerGas: 2_000_000_000n, gasLimit: 65_000n });
  assert.equal(hex(erc20Transfer("0x5aAeb6053F3E94C9b9A09f33669435E7Ef1BeAed", 1_500_000n)), "a9059cbb0000000000000000000000005aaeb6053f3e94c9b9a09f33669435e7ef1beaed000000000000000000000000000000000000000000000000000000000016e360");
  const { raw, hash } = signEip1559(tx, keys.evm.secret);
  assert.ok(raw.startsWith("0x02"));
  assert.equal(hash, "0x" + hex(keccak_256(hexToBytes(raw.slice(2)))));
  // Recover the sender from the signature the transaction carries: it is the vault's address.
  const sig = secp256k1.sign(signingHash(tx), keys.evm.secret);
  const pub = sig.recoverPublicKey(signingHash(tx)).toRawBytes(false);
  assert.equal("0x" + hex(keccak_256(pub.slice(1)).slice(12)), keys.evm.address.toLowerCase());
});

test("the ownership proof verifies with both public keys", () => {
  const keys = deriveKeys(ABANDON);
  const proof = proveOwnership("user_1", keys, new Date("2026-10-29T00:00:00Z"));
  assert.equal(proof.message, ownershipMessage("user_1", keys.solana.address, keys.evm.address, "2026-10-29T00:00:00.000Z"));
  assert.ok(ed25519.verify(base58.decode(proof.solana_signature), utf8ToBytes(proof.message), base58.decode(keys.solana.address)));
  const sigBytes = hexToBytes(proof.evm_signature.slice(2));
  assert.equal(sigBytes.length, 65);
  const body = utf8ToBytes(proof.message);
  const digest = keccak_256(concatBytes(utf8ToBytes(`\x19Ethereum Signed Message:\n${body.length}`), body));
  const recovered = secp256k1.Signature.fromCompact(sigBytes.slice(0, 64)).addRecoveryBit(sigBytes[64] - 27).recoverPublicKey(digest).toRawBytes(false);
  assert.equal("0x" + hex(keccak_256(recovered.slice(1)).slice(12)), keys.evm.address.toLowerCase());
  assert.equal(personalSign("hi", keys.evm.secret).length, 132);
  assert.equal(evmAddressOf(keys.evm.secret), keys.evm.address);
});

test("Solana transfers: the associated account matches the Go code, and the signature covers the message", () => {
  const owner = "HAgk14JpMQLgt6rVgv7cBQFJWFto5Dqxi472uT3DKpqk";
  assert.equal(deriveKeys(ABANDON).solana.address, owner);
  assert.equal(associatedTokenAddress(owner, "4zMMC9srt5Ri5X14GAgXhaHii3GnPAEERYPJgZJDncDU"), "Bjr2Shw9sy31UNdm8kYs5y72iTbnxB4JemK98vJqyQWm");
  const keys = deriveKeys(ABANDON);
  const built = tokenTransfer({ owner, to: "GhpmTWT8ng3viaQtFNDF7T7RyrmemosWvEDetKmBEoPA", mint: "4zMMC9srt5Ri5X14GAgXhaHii3GnPAEERYPJgZJDncDU", decimals: 6, amount: 1_500_000n, blockhash: "EkSnNWid2cvwEVnVx9aBqawnmiCNiDgp3gUdkDPTKN1N", createRecipientAccount: false });
  const { raw } = signSolana(built.message, keys.solana.secret);
  const bytes = base64.decode(raw);
  assert.equal(bytes[0], 1);
  assert.ok(ed25519.verify(bytes.slice(1, 65), bytes.slice(65), base58.decode(owner)));
  assert.throws(() => tokenTransfer({ owner, to: owner, mint: "4zMMC9srt5Ri5X14GAgXhaHii3GnPAEERYPJgZJDncDU", decimals: 6, amount: 1n, blockhash: "EkSnNWid2cvwEVnVx9aBqawnmiCNiDgp3gUdkDPTKN1N", createRecipientAccount: false }), /own address/);
  assert.throws(() => solTransfer({ owner, to: "GhpmTWT8ng3viaQtFNDF7T7RyrmemosWvEDetKmBEoPA", lamports: 0n, blockhash: "EkSnNWid2cvwEVnVx9aBqawnmiCNiDgp3gUdkDPTKN1N" }), /more than zero/);
});

test("amounts convert exactly", () => {
  assert.equal(toMinor("1.25", 6), 1_250_000n);
  assert.equal(toMinor("0.0000001", 6), null);
  assert.equal(toMinor("1e3", 6), null);
  assert.equal(fromMinor(1_250_000n, 6), "1.25");
  assert.equal(fromMinor(5_000_000_000n, 9), "5");
  assert.equal(fromMinor(1n, 18, 6), "0");
});

import { buildWithdrawal, FeeTooHighError } from "../lib/wallet/withdraw.ts";
import { vaultChain } from "../lib/wallet/chains.ts";

test("a withdrawal is built from the browser's own choices; bad server facts are refused", () => {
  const keys = deriveKeys(ABANDON);
  const base = vaultChain("base-sepolia");
  const facts = { evm: { chain_id: 84532, nonce: 0, max_priority_fee_per_gas: 1_000_000, max_fee_per_gas: 2_000_000_000, gas_limit: 65_000 } };
  const w = buildWithdrawal(keys, base, "0x5aAeb6053F3E94C9b9A09f33669435E7Ef1BeAed", 1_000_000n, facts);
  assert.ok(w.raw.startsWith("0x02") && w.id.startsWith("0x"));
  assert.throws(() => buildWithdrawal(keys, base, "0x5aAeb6053F3E94C9b9A09f33669435E7Ef1BeAed", 1n, { evm: { ...facts.evm, chain_id: 1 } }), /different chain/);
  assert.throws(() => buildWithdrawal(keys, base, "0x5aAeb6053F3E94C9b9A09f33669435E7Ef1BeAed", 1n, { evm: { ...facts.evm, max_fee_per_gas: 1_000_000_000_000 } }), FeeTooHighError);
  const sol = vaultChain("solana-devnet");
  const s = buildWithdrawal(keys, sol, "GhpmTWT8ng3viaQtFNDF7T7RyrmemosWvEDetKmBEoPA", 1n, { blockhash: "EkSnNWid2cvwEVnVx9aBqawnmiCNiDgp3gUdkDPTKN1N", recipient_has_token_account: true });
  assert.ok(base58.decode(s.id).length === 64);
  assert.throws(() => buildWithdrawal(keys, sol, "GhpmTWT8ng3viaQtFNDF7T7RyrmemosWvEDetKmBEoPA", 1n, {}), /recent block/);
});
