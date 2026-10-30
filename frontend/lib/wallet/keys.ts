// The owner's keys, made and used only in this browser (docs/WALLET.md). One
// BIP-39 phrase gives a Solana key (SLIP-0010 ed25519, Phantom's path) and an
// EVM key (BIP-32 secp256k1) whose address receives on Base and Arc.

import { generateMnemonic, mnemonicToSeedSync, validateMnemonic } from "@scure/bip39";
import { wordlist } from "@scure/bip39/wordlists/english";
import { HDKey } from "@scure/bip32";
import { ed25519 } from "@noble/curves/ed25519";
import { secp256k1 } from "@noble/curves/secp256k1";
import { keccak_256 } from "@noble/hashes/sha3";
import { hmac } from "@noble/hashes/hmac";
import { sha512 } from "@noble/hashes/sha2";
import { bytesToHex, utf8ToBytes } from "@noble/hashes/utils";
import { base58 } from "@scure/base";

/** Solana's path, as Phantom and Solflare derive it: the phrase imports there unchanged. */
export const SOLANA_PATH = "m/44'/501'/0'/0'";
/** The first EVM account, as every EVM wallet derives it. */
export const EVM_PATH = "m/44'/60'/0'/0/0";

export type OwnerKeys = {
  solana: { secret: Uint8Array; publicKey: Uint8Array; address: string };
  evm: { secret: Uint8Array; address: string };
};

/** A new 12-word phrase (128 bits from the platform's secure random source). */
export function newMnemonic(): string {
  return generateMnemonic(wordlist, 128);
}

/** A phrase as people type it: case and spacing don't matter. */
export function normalizeMnemonic(phrase: string): string {
  return phrase.trim().toLowerCase().split(/\s+/).join(" ");
}

export function isMnemonic(phrase: string): boolean {
  return validateMnemonic(normalizeMnemonic(phrase), wordlist);
}

/** SLIP-0010 ed25519 derivation (every step hardened, as ed25519 requires). */
export function slip10Ed25519(seed: Uint8Array, path: string): Uint8Array {
  let I = hmac(sha512, utf8ToBytes("ed25519 seed"), seed);
  let key = I.slice(0, 32);
  let chainCode = I.slice(32);
  const parts = path.split("/");
  if (parts[0] !== "m") throw new Error("A derivation path starts at m");
  for (const part of parts.slice(1)) {
    if (!/^\d+'$/.test(part)) throw new Error("ed25519 derives hardened steps only");
    const index = (Number(part.slice(0, -1)) + 0x80000000) >>> 0;
    const data = new Uint8Array(37);
    data.set(key, 1);
    new DataView(data.buffer).setUint32(33, index);
    I = hmac(sha512, chainCode, data);
    key = I.slice(0, 32);
    chainCode = I.slice(32);
  }
  return key;
}

/** EIP-55: the mixed-case checksum form of an EVM address. */
export function checksumAddress(address: string): string {
  const lower = address.toLowerCase().replace(/^0x/, "");
  const hash = bytesToHex(keccak_256(utf8ToBytes(lower)));
  let out = "0x";
  for (let i = 0; i < lower.length; i++) out += parseInt(hash[i], 16) >= 8 ? lower[i].toUpperCase() : lower[i];
  return out;
}

/** The EVM address of a secp256k1 key: the last 20 bytes of keccak256 of the uncompressed public key. */
export function evmAddressOf(secret: Uint8Array): string {
  const pub = secp256k1.getPublicKey(secret, false);
  return checksumAddress(bytesToHex(keccak_256(pub.slice(1)).slice(12)));
}

export function isEvmAddress(value: string): boolean {
  return /^0x[0-9a-fA-F]{40}$/.test(value) && (value === value.toLowerCase() || value.slice(2) === value.slice(2).toUpperCase() || checksumAddress(value) === value);
}

export function isSolanaAddress(value: string): boolean {
  try {
    return base58.decode(value).length === 32;
  } catch {
    return false;
  }
}

/** Both keys from a phrase. The seed is wiped once they are made. */
export function deriveKeys(phrase: string): OwnerKeys {
  const seed = mnemonicToSeedSync(normalizeMnemonic(phrase));
  try {
    const solSecret = slip10Ed25519(seed, SOLANA_PATH);
    const solPublic = ed25519.getPublicKey(solSecret);
    const node = HDKey.fromMasterSeed(seed).derive(EVM_PATH);
    if (!node.privateKey) throw new Error("The EVM key could not be derived");
    const evmSecret = node.privateKey.slice();
    node.wipePrivateData();
    return {
      solana: { secret: solSecret, publicKey: solPublic, address: base58.encode(solPublic) },
      evm: { secret: evmSecret, address: evmAddressOf(evmSecret) },
    };
  } finally {
    seed.fill(0);
  }
}

/** Overwrites the secrets: for when the wallet locks. */
export function wipeKeys(keys: OwnerKeys | null) {
  keys?.solana.secret.fill(0);
  keys?.evm.secret.fill(0);
}
