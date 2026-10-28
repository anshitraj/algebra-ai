// Proof that an account's vault addresses belong to whoever holds the phrase:
// one message naming the account and both addresses, signed by both keys.
// The server checks both signatures before it records the addresses.

import { ed25519 } from "@noble/curves/ed25519";
import { secp256k1 } from "@noble/curves/secp256k1";
import { keccak_256 } from "@noble/hashes/sha3";
import { bytesToHex, concatBytes, utf8ToBytes } from "@noble/hashes/utils";
import { base58 } from "@scure/base";
import type { OwnerKeys } from "./keys.ts";

export type OwnershipProof = {
  solana_address: string;
  evm_address: string;
  message: string;
  solana_signature: string;
  evm_signature: string;
};

/** The message both keys sign. The server rebuilds and compares it, so its wording is part of the protocol. */
export function ownershipMessage(userId: string, solana: string, evm: string, issued: string): string {
  return ["Algebra vault ownership", `Account: ${userId}`, `Solana: ${solana}`, `EVM: ${evm}`, `Issued: ${issued}`].join("\n");
}

/** EIP-191 personal_sign: r || s || v, v being 27 or 28. */
export function personalSign(message: string, secret: Uint8Array): string {
  const body = utf8ToBytes(message);
  const hash = keccak_256(concatBytes(utf8ToBytes(`\x19Ethereum Signed Message:\n${body.length}`), body));
  const sig = secp256k1.sign(hash, secret);
  return "0x" + bytesToHex(sig.toCompactRawBytes()) + (27 + sig.recovery).toString(16);
}

export function proveOwnership(userId: string, keys: OwnerKeys, now = new Date()): OwnershipProof {
  const message = ownershipMessage(userId, keys.solana.address, keys.evm.address, now.toISOString());
  return {
    solana_address: keys.solana.address,
    evm_address: keys.evm.address,
    message,
    solana_signature: base58.encode(ed25519.sign(utf8ToBytes(message), keys.solana.secret)),
    evm_signature: personalSign(message, keys.evm.secret),
  };
}
