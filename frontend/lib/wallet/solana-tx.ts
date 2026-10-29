// Solana transactions the browser builds and signs itself: a USDC transfer
// (SPL TransferChecked, creating the recipient's token account first when it
// has none), a SOL transfer, and the agent allowance (SPL ApproveChecked to
// Algebra's payer, or Revoke). The server supplies only a recent blockhash,
// whether the recipient's token account exists and, for the allowance, the
// payer's address; it relays the result.

import { ed25519 } from "@noble/curves/ed25519";
import { sha256 } from "@noble/hashes/sha2";
import { concatBytes, utf8ToBytes } from "@noble/hashes/utils";
import { base58, base64 } from "@scure/base";

export const SYSTEM_PROGRAM = "11111111111111111111111111111111";
export const TOKEN_PROGRAM = "TokenkegQfeZyiNwAJbNbGKPFXCWuBvf9Ss623VQ5DA";
export const ATA_PROGRAM = "ATokenGPvbdGVxr1b2hvZbsiqW5xWH25efTNsLJA8knL";
export const COMPUTE_BUDGET_PROGRAM = "ComputeBudget111111111111111111111111111111";

const key = (b58: string) => {
  const k = base58.decode(b58);
  if (k.length !== 32) throw new Error("Not a Solana address");
  return k;
};

function onCurve(bytes: Uint8Array): boolean {
  try {
    ed25519.ExtendedPoint.fromHex(bytes);
    return true;
  } catch {
    return false;
  }
}

/** The program-derived address for seeds, with the highest bump that is off the curve. */
export function findProgramAddress(seeds: Uint8Array[], program: string): string {
  const pid = key(program);
  for (let bump = 255; bump >= 0; bump--) {
    const h = sha256(concatBytes(...seeds, Uint8Array.of(bump), pid, utf8ToBytes("ProgramDerivedAddress")));
    if (!onCurve(h)) return base58.encode(h);
  }
  throw new Error("No program address for these seeds");
}

/** An owner's associated token account for a mint. */
export function associatedTokenAddress(owner: string, mint: string): string {
  return findProgramAddress([key(owner), key(TOKEN_PROGRAM), key(mint)], ATA_PROGRAM);
}

/** Solana's compact-u16 length. */
function shortvec(n: number): Uint8Array {
  const out: number[] = [];
  let v = n;
  for (;;) {
    let b = v & 0x7f;
    v >>= 7;
    if (v) b |= 0x80;
    out.push(b);
    if (!v) return Uint8Array.from(out);
  }
}

function u64(n: bigint): Uint8Array {
  const b = new Uint8Array(8);
  new DataView(b.buffer).setBigUint64(0, n, true);
  return b;
}

function u32(n: number): Uint8Array {
  const b = new Uint8Array(4);
  new DataView(b.buffer).setUint32(0, n, true);
  return b;
}

type Ix = { program: string; accounts: string[]; data: Uint8Array };

/**
 * A legacy message. accounts are ordered as the runtime requires: the
 * signers (the first pays the fee; the last readonlySigners of them only
 * sign), then writable accounts, then read-only ones.
 */
function compileSigned(signers: string[], readonlySigners: number, writable: string[], readonly: string[], blockhash: string, ixs: Ix[]): Uint8Array {
  const keys = [...signers, ...writable, ...readonly];
  if (new Set(keys).size !== keys.length) throw new Error("An account appears twice");
  const index = (k: string) => {
    const i = keys.indexOf(k);
    if (i < 0) throw new Error(`Account ${k} is missing from the message`);
    return i;
  };
  const header = Uint8Array.of(signers.length, readonlySigners, readonly.length);
  const compiled = ixs.map((ix) => concatBytes(Uint8Array.of(index(ix.program)), shortvec(ix.accounts.length), Uint8Array.from(ix.accounts.map(index)), shortvec(ix.data.length), ix.data));
  return concatBytes(header, shortvec(keys.length), ...keys.map(key), key(blockhash), shortvec(ixs.length), ...compiled);
}

/** A legacy message with one signer, who pays the fee. */
function compile(signer: string, writable: string[], readonly: string[], blockhash: string, ixs: Ix[]): Uint8Array {
  return compileSigned([signer], 0, writable, readonly, blockhash, ixs);
}

export type BuiltTx = { message: Uint8Array; summary: string };

/** A USDC (or any SPL token) transfer from owner to recipient's associated account. */
export function tokenTransfer(p: { owner: string; to: string; mint: string; decimals: number; amount: bigint; blockhash: string; createRecipientAccount: boolean }): BuiltTx {
  if (p.amount <= 0n) throw new Error("Send more than zero");
  if (p.to === p.owner) throw new Error("That is this vault's own address");
  const source = associatedTokenAddress(p.owner, p.mint);
  const dest = associatedTokenAddress(p.to, p.mint);
  const ixs: Ix[] = [];
  if (p.createRecipientAccount) {
    // CreateIdempotent: harmless if someone made the account in the meantime.
    ixs.push({ program: ATA_PROGRAM, accounts: [p.owner, dest, p.to, p.mint, SYSTEM_PROGRAM, TOKEN_PROGRAM], data: Uint8Array.of(1) });
  }
  ixs.push({ program: TOKEN_PROGRAM, accounts: [source, p.mint, dest, p.owner], data: concatBytes(Uint8Array.of(12), u64(p.amount), Uint8Array.of(p.decimals)) });
  const readonly = p.createRecipientAccount ? [p.mint, p.to, SYSTEM_PROGRAM, TOKEN_PROGRAM, ATA_PROGRAM] : [p.mint, TOKEN_PROGRAM];
  return { message: compile(p.owner, [source, dest], readonly, p.blockhash, ixs), summary: `${p.amount} to ${p.to}` };
}

/** A SOL transfer. */
export function solTransfer(p: { owner: string; to: string; lamports: bigint; blockhash: string }): BuiltTx {
  if (p.lamports <= 0n) throw new Error("Send more than zero");
  if (p.to === p.owner) throw new Error("That is this vault's own address");
  const data = concatBytes(Uint8Array.of(2, 0, 0, 0), u64(p.lamports));
  return { message: compile(p.owner, [p.to], [SYSTEM_PROGRAM], p.blockhash, [{ program: SYSTEM_PROGRAM, accounts: [p.owner, p.to], data }]), summary: `${p.lamports} lamports to ${p.to}` };
}

/** The most compute an allowance may ask for, and the most it may pay per unit: what the server accepts. */
export const ALLOWANCE_UNITS = 20_000;
export const MAX_ALLOWANCE_UNIT_PRICE = 1_000_000n;

/**
 * The agent allowance: an SPL ApproveChecked naming Algebra's payer as the
 * delegate of the vault's USDC account for amount (replacing any allowance
 * before it), or, with amount null, a Revoke. The payer pays the fee, so it
 * signs first; the vault signs second. The token program will never let the
 * delegate move more than what is left of amount.
 */
export function allowanceTx(p: { feePayer: string; owner: string; mint: string; decimals: number; amount: bigint | null; blockhash: string; unitLimit: number; unitPrice: bigint }): BuiltTx {
  if (p.feePayer === p.owner) throw new Error("The vault can't be its own delegate");
  if (p.amount !== null && p.amount <= 0n) throw new Error("Allow more than zero, or stop the allowance");
  if (p.unitLimit > ALLOWANCE_UNITS || p.unitPrice > MAX_ALLOWANCE_UNIT_PRICE || p.unitPrice < 0n) throw new Error("The network fee the server suggested is far above normal, so nothing was signed");
  const source = associatedTokenAddress(p.owner, p.mint);
  const budget: Ix[] = [
    { program: COMPUTE_BUDGET_PROGRAM, accounts: [], data: concatBytes(Uint8Array.of(2), u32(p.unitLimit)) },
    { program: COMPUTE_BUDGET_PROGRAM, accounts: [], data: concatBytes(Uint8Array.of(3), u64(p.unitPrice)) },
  ];
  if (p.amount === null) {
    const revoke: Ix = { program: TOKEN_PROGRAM, accounts: [source, p.owner], data: Uint8Array.of(5) };
    return { message: compileSigned([p.feePayer, p.owner], 1, [source], [TOKEN_PROGRAM, COMPUTE_BUDGET_PROGRAM], p.blockhash, [...budget, revoke]), summary: "stop the allowance" };
  }
  // The delegate is the payer itself, already in the message as fee payer.
  const approve: Ix = { program: TOKEN_PROGRAM, accounts: [source, p.mint, p.feePayer, p.owner], data: concatBytes(Uint8Array.of(13), u64(p.amount), Uint8Array.of(p.decimals)) };
  return { message: compileSigned([p.feePayer, p.owner], 1, [source], [p.mint, TOKEN_PROGRAM, COMPUTE_BUDGET_PROGRAM], p.blockhash, [...budget, approve]), summary: `allow ${p.amount} to ${p.feePayer}` };
}

/** Signs as the second of two signers: the transaction with the fee payer's slot left empty, for the fee payer to fill. */
export function signAsSecond(message: Uint8Array, secret: Uint8Array): string {
  const sig = ed25519.sign(message, secret);
  return base64.encode(concatBytes(shortvec(2), new Uint8Array(64), sig, message));
}

/** Signs a one-signer message: the base64 transaction to send and its signature (the transaction's ID). */
export function signSolana(message: Uint8Array, secret: Uint8Array): { raw: string; signature: string } {
  const sig = ed25519.sign(message, secret);
  return { raw: base64.encode(concatBytes(shortvec(1), sig, message)), signature: base58.encode(sig) };
}
