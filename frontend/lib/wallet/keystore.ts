// The sealed phrase (docs/WALLET.md): AES-256-GCM under a key Argon2id derives
// from the wallet password, bound to its two addresses. Kept in this browser
// and in a backup file the owner downloads; never sent to Algebra.

import { argon2idAsync } from "@noble/hashes/argon2";
import { utf8ToBytes } from "@noble/hashes/utils";
import { base64 } from "@scure/base";
import { deriveKeys, normalizeMnemonic, wipeKeys } from "./keys.ts";

export type Addresses = { solana: string; evm: string };

export type Keystore = {
  type: "algebra-vault-keystore";
  version: 1;
  kdf: { name: "argon2id"; t: number; m: number; p: number; salt: string };
  cipher: { name: "aes-256-gcm"; iv: string; ciphertext: string };
  addresses: Addresses;
  created_at: string;
};

/** Argon2id cost: 3 passes over 32 MiB, one lane (above OWASP's minimum; about two seconds in a browser). */
export const KDF = { t: 3, m: 32768, p: 1 } as const;

/** The shortest wallet password accepted. */
export const MIN_PASSWORD = 10;

export class WrongPasswordError extends Error {
  constructor() {
    super("That password doesn't open this wallet");
  }
}

/** WebCrypto wants bytes over a plain ArrayBuffer: a copy guarantees one. */
const buf = (b: Uint8Array) => new Uint8Array(b);

function aad(a: Addresses) {
  return buf(utf8ToBytes(`algebra-vault-keystore-v1|${a.solana}|${a.evm}`));
}

async function aesKey(password: string, salt: Uint8Array, kdf: { t: number; m: number; p: number }, onProgress?: (p: number) => void) {
  const raw = await argon2idAsync(utf8ToBytes(password.normalize("NFKC")), salt, { ...kdf, dkLen: 32, onProgress });
  try {
    return await crypto.subtle.importKey("raw", buf(raw), "AES-GCM", false, ["encrypt", "decrypt"]);
  } finally {
    raw.fill(0);
  }
}

/** Seals a phrase under a password. The addresses are derived here, so they always match the phrase. */
export async function seal(phrase: string, password: string, onProgress?: (p: number) => void): Promise<Keystore> {
  if (password.length < MIN_PASSWORD) throw new Error(`Use at least ${MIN_PASSWORD} characters`);
  const normalized = normalizeMnemonic(phrase);
  const keys = deriveKeys(normalized);
  const addresses = { solana: keys.solana.address, evm: keys.evm.address };
  wipeKeys(keys);
  const salt = crypto.getRandomValues(new Uint8Array(16));
  const iv = crypto.getRandomValues(new Uint8Array(12));
  const key = await aesKey(password, salt, KDF, onProgress);
  const plain = buf(utf8ToBytes(JSON.stringify({ mnemonic: normalized })));
  const sealed = new Uint8Array(await crypto.subtle.encrypt({ name: "AES-GCM", iv, additionalData: aad(addresses) }, key, plain));
  plain.fill(0);
  return {
    type: "algebra-vault-keystore",
    version: 1,
    kdf: { name: "argon2id", ...KDF, salt: base64.encode(salt) },
    cipher: { name: "aes-256-gcm", iv: base64.encode(iv), ciphertext: base64.encode(sealed) },
    addresses,
    created_at: new Date().toISOString(),
  };
}

/** Whether a value is a keystore this version reads (a backup file, a stored copy). */
export function isKeystore(v: unknown): v is Keystore {
  const k = v as Keystore;
  return !!k && k.type === "algebra-vault-keystore" && k.version === 1 && k.kdf?.name === "argon2id" && k.cipher?.name === "aes-256-gcm" && typeof k.addresses?.solana === "string" && typeof k.addresses?.evm === "string";
}

/**
 * Opens a keystore. A wrong password and an edited keystore both fail the
 * authenticated decryption; the phrase is then derived again and must give
 * the addresses the keystore names.
 */
export async function open(ks: Keystore, password: string, onProgress?: (p: number) => void): Promise<string> {
  if (!isKeystore(ks)) throw new Error("This isn't an Algebra vault backup");
  // Bounded costs: a planted keystore can't ask for gigabytes of memory.
  if (ks.kdf.m > 1 << 20 || ks.kdf.t > 10 || ks.kdf.p > 4) throw new Error("This backup asks for an unsafe amount of work");
  const key = await aesKey(password, base64.decode(ks.kdf.salt), ks.kdf, onProgress);
  let plain: Uint8Array;
  try {
    plain = new Uint8Array(await crypto.subtle.decrypt({ name: "AES-GCM", iv: buf(base64.decode(ks.cipher.iv)), additionalData: aad(ks.addresses) }, key, buf(base64.decode(ks.cipher.ciphertext))));
  } catch {
    throw new WrongPasswordError();
  }
  const { mnemonic } = JSON.parse(new TextDecoder().decode(plain)) as { mnemonic: string };
  plain.fill(0);
  const keys = deriveKeys(mnemonic);
  const same = keys.solana.address === ks.addresses.solana && keys.evm.address === ks.addresses.evm;
  wipeKeys(keys);
  if (!same) throw new Error("This backup's addresses don't match its phrase");
  return mnemonic;
}

const slot = (userId: string) => `algebra:vault-keystore:${userId}`;

/** The keystore kept in this browser for an account, if any. */
export function loadKeystore(userId: string): Keystore | null {
  try {
    const raw = window.localStorage.getItem(slot(userId));
    const v = raw ? (JSON.parse(raw) as unknown) : null;
    return isKeystore(v) ? v : null;
  } catch {
    return null;
  }
}

/** Keeps the keystore in this browser. False when storage is blocked: the backup file is then the only copy. */
export function saveKeystore(userId: string, ks: Keystore): boolean {
  try {
    window.localStorage.setItem(slot(userId), JSON.stringify(ks));
    return true;
  } catch {
    return false;
  }
}

export function forgetKeystore(userId: string) {
  try {
    window.localStorage.removeItem(slot(userId));
  } catch {
    // nothing stored
  }
}

/** The backup file's contents: the same sealed keystore, readable by "Import a backup". */
export function backupFile(ks: Keystore): Blob {
  return new Blob([JSON.stringify(ks, null, 2)], { type: "application/json" });
}
