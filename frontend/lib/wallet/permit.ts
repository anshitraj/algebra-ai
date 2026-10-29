// The agent allowance on Base and Arc, signed in this browser: an EIP-2612
// permit that lets Algebra's payer move up to an amount of the vault's USDC,
// or a permit for 0, which stops it. Signing sends nothing and costs the vault
// nothing: Algebra submits the permit and pays its gas. The server names the
// payer and the permit's nonce; this browser checks the rest against what it
// knows itself (the chain, its USDC contract and that token's EIP-712 domain,
// the vault's own address) and signs only what it built.

import { secp256k1 } from "@noble/curves/secp256k1";
import { keccak_256 } from "@noble/hashes/sha3";
import { bytesToHex, concatBytes, hexToBytes, utf8ToBytes } from "@noble/hashes/utils";
import type { OwnerKeys } from "./keys.ts";
import { fromMinor, vaultChain } from "./chains.ts";

export type PermitFacts = {
  network: string;
  chain_id: number;
  token: string;
  name: string;
  version: string;
  owner: string;
  spender: string;
  /** The permit nonce, as a decimal string. */
  nonce: string;
  deadline: number;
  max_minor: number;
};

/** USDC's EIP-712 domain on each chain, as the contracts report it (name(), version()). */
const DOMAINS: Record<string, { name: string; version: string }> = {
  "base-sepolia": { name: "USDC", version: "2" },
  "arc-testnet": { name: "USDC", version: "2" },
  base: { name: "USD Coin", version: "2" },
  arc: { name: "USDC", version: "2" },
};

const DOMAIN_TYPE = keccak_256(utf8ToBytes("EIP712Domain(string name,string version,uint256 chainId,address verifyingContract)"));
const PERMIT_TYPE = keccak_256(utf8ToBytes("Permit(address owner,address spender,uint256 value,uint256 nonce,uint256 deadline)"));

function word(n: bigint): Uint8Array {
  if (n < 0n || n >= 1n << 256n) throw new Error("A value doesn't fit in 256 bits");
  return hexToBytes(n.toString(16).padStart(64, "0"));
}

function addressWord(a: string): Uint8Array {
  if (!/^0x[0-9a-fA-F]{40}$/.test(a)) throw new Error(`${a} isn't an address`);
  return concatBytes(new Uint8Array(12), hexToBytes(a.slice(2).toLowerCase()));
}

/** The digest a permit signs (EIP-712). */
export function permitDigest(p: { chainId: number; token: string; name: string; version: string; owner: string; spender: string; value: bigint; nonce: bigint; deadline: bigint }): Uint8Array {
  const domain = keccak_256(concatBytes(DOMAIN_TYPE, keccak_256(utf8ToBytes(p.name)), keccak_256(utf8ToBytes(p.version)), word(BigInt(p.chainId)), addressWord(p.token)));
  const struct = keccak_256(concatBytes(PERMIT_TYPE, addressWord(p.owner), addressWord(p.spender), word(p.value), word(p.nonce), word(p.deadline)));
  return keccak_256(concatBytes(Uint8Array.of(0x19, 0x01), domain, struct));
}

/** Signs a digest: r || s || v, with v 27 or 28. */
export function signDigest(digest: Uint8Array, secret: Uint8Array): string {
  const sig = secp256k1.sign(digest, secret);
  return "0x" + bytesToHex(concatBytes(word(sig.r), word(sig.s), Uint8Array.of(27 + sig.recovery)));
}

/** The signed permit for a limit of amount (0 stops agents spending), after checking the server's facts. */
export function signPermit(keys: OwnerKeys, facts: PermitFacts, amount: bigint): { signature: string; value: bigint; deadline: number } {
  const chain = vaultChain(facts.network);
  const domain = DOMAINS[facts.network];
  if (!chain || chain.kind !== "evm" || !domain) throw new Error("That chain doesn't take a permit here");
  if (facts.chain_id !== chain.chainId) throw new Error("The server named another chain, so nothing was signed");
  if (facts.token.toLowerCase() !== chain.usdc.toLowerCase()) throw new Error("The server named a token other than USDC, so nothing was signed");
  if (facts.name !== domain.name || facts.version !== domain.version) throw new Error("The server named another token domain, so nothing was signed");
  if (facts.owner.toLowerCase() !== keys.evm.address.toLowerCase()) throw new Error("The server named an owner that isn't this vault, so nothing was signed");
  if (!/^0x[0-9a-fA-F]{40}$/.test(facts.spender) || facts.spender.toLowerCase() === keys.evm.address.toLowerCase()) throw new Error("The server named no usable spender, so nothing was signed");
  if (!/^\d+$/.test(facts.nonce)) throw new Error("The server's nonce isn't a number, so nothing was signed");
  if (amount < 0n || amount > BigInt(facts.max_minor)) throw new Error(`At most ${fromMinor(BigInt(facts.max_minor), 6)} USDC`);
  const now = Math.floor(Date.now() / 1000);
  if (facts.deadline < now + 3600 || facts.deadline > now + 400 * 86400) throw new Error("The permit's deadline is out of range, so nothing was signed");
  const digest = permitDigest({
    chainId: facts.chain_id, token: facts.token, name: facts.name, version: facts.version, owner: keys.evm.address, spender: facts.spender,
    value: amount, nonce: BigInt(facts.nonce), deadline: BigInt(facts.deadline),
  });
  return { signature: signDigest(digest, keys.evm.secret), value: amount, deadline: facts.deadline };
}
