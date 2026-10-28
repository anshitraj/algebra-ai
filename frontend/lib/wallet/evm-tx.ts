// EVM transactions the browser builds and signs itself: an ERC-20 USDC
// transfer in an EIP-1559 (type 2) transaction, on Base or Arc. The server
// supplies only the nonce, the fees and the gas limit, and relays the result.

import { secp256k1 } from "@noble/curves/secp256k1";
import { keccak_256 } from "@noble/hashes/sha3";
import { bytesToHex, concatBytes, hexToBytes } from "@noble/hashes/utils";

type RlpItem = Uint8Array | RlpItem[];

/** A non-negative integer as minimal big-endian bytes (zero is empty), as RLP wants it. */
export function intBytes(n: bigint): Uint8Array {
  if (n < 0n) throw new Error("RLP integers are non-negative");
  if (n === 0n) return new Uint8Array();
  let hex = n.toString(16);
  if (hex.length % 2) hex = "0" + hex;
  return hexToBytes(hex);
}

function lengthPrefix(len: number, short: number, long: number): Uint8Array {
  if (len <= 55) return Uint8Array.of(short + len);
  const l = intBytes(BigInt(len));
  return concatBytes(Uint8Array.of(long + l.length), l);
}

export function rlp(item: RlpItem): Uint8Array {
  if (item instanceof Uint8Array) {
    if (item.length === 1 && item[0] < 0x80) return item;
    return concatBytes(lengthPrefix(item.length, 0x80, 0xb7), item);
  }
  const body = concatBytes(...item.map(rlp));
  return concatBytes(lengthPrefix(body.length, 0xc0, 0xf7), body);
}

function address20(addr: string): Uint8Array {
  if (!/^0x[0-9a-fA-F]{40}$/.test(addr)) throw new Error("Not an EVM address");
  return hexToBytes(addr.slice(2));
}

function word(bytes: Uint8Array): Uint8Array {
  const out = new Uint8Array(32);
  out.set(bytes, 32 - bytes.length);
  return out;
}

/** ERC-20 transfer(to, amount) call data. */
export function erc20Transfer(to: string, amount: bigint): Uint8Array {
  return concatBytes(hexToBytes("a9059cbb"), word(address20(to)), word(intBytes(amount)));
}

export type Eip1559 = {
  chainId: number;
  nonce: bigint;
  maxPriorityFeePerGas: bigint;
  maxFeePerGas: bigint;
  gasLimit: bigint;
  to: string;
  value: bigint;
  data: Uint8Array;
};

function fields(tx: Eip1559): RlpItem[] {
  return [intBytes(BigInt(tx.chainId)), intBytes(tx.nonce), intBytes(tx.maxPriorityFeePerGas), intBytes(tx.maxFeePerGas), intBytes(tx.gasLimit), address20(tx.to), intBytes(tx.value), tx.data, []];
}

/** The hash a type-2 transaction's sender signs. */
export function signingHash(tx: Eip1559): Uint8Array {
  return keccak_256(concatBytes(Uint8Array.of(2), rlp(fields(tx))));
}

/** Signs a type-2 transaction: the raw transaction to broadcast and its hash. */
export function signEip1559(tx: Eip1559, secret: Uint8Array): { raw: string; hash: string } {
  const sig = secp256k1.sign(signingHash(tx), secret);
  const signed = concatBytes(Uint8Array.of(2), rlp([...fields(tx), intBytes(BigInt(sig.recovery)), intBytes(sig.r), intBytes(sig.s)]));
  return { raw: "0x" + bytesToHex(signed), hash: "0x" + bytesToHex(keccak_256(signed)) };
}

/** A USDC transfer on an EVM chain, ready to sign: the transaction goes to the token contract with no value. */
export function usdcTransferTx(p: { chainId: number; usdc: string; to: string; amount: bigint; nonce: bigint; maxPriorityFeePerGas: bigint; maxFeePerGas: bigint; gasLimit: bigint }): Eip1559 {
  return { chainId: p.chainId, nonce: p.nonce, maxPriorityFeePerGas: p.maxPriorityFeePerGas, maxFeePerGas: p.maxFeePerGas, gasLimit: p.gasLimit, to: p.usdc, value: 0n, data: erc20Transfer(p.to, p.amount) };
}
