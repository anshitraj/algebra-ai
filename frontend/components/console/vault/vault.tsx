"use client";

// The Vault page (docs/WALLET.md): the owner's own wallet, made and unlocked
// in this browser. Balances on Solana, Base and Arc; deposit addresses on
// Testnet (mainnet deposits wait for an independent review); withdrawals
// built and signed here; the phrase and the encrypted backup in settings.

import { useCallback, useEffect, useMemo, useState } from "react";
import * as api from "@/lib/api-client";
import { useSession } from "@/lib/session";
import { useNetwork } from "@/lib/network";
import { checksumAddress, deriveKeys, isEvmAddress, isMnemonic, isSolanaAddress, newMnemonic, normalizeMnemonic } from "@/lib/wallet/keys";
import { backupFile, forgetKeystore, isKeystore, loadKeystore, MIN_PASSWORD, open, saveKeystore, seal, WrongPasswordError, type Keystore } from "@/lib/wallet/keystore";
import { proveOwnership } from "@/lib/wallet/proof";
import { lockVault, unlockedKeys, unlockWith, useVaultUnlocked } from "@/lib/wallet/session";
import { fromMinor, toMinor, vaultChain, vaultChains, type VaultChain } from "@/lib/wallet/chains";
import { buildWithdrawal } from "@/lib/wallet/withdraw";
import { buildAllowance } from "@/lib/wallet/allowance";
import { ChainMark } from "@/components/chain-logos";
import { Button, ErrorNote, Field, Input, PageHeader, Skeleton } from "@/components/console/ui";
import { IconArrowRight, IconCheck, IconCopy, IconExternal, IconLock, IconRefresh, IconShield, IconUnlock, Spinner } from "@/components/icons";
import styles from "./vault.module.css";

type Panel = null | "deposit" | "withdraw" | "settings";

const short = (a: string) => (a.length > 14 ? `${a.slice(0, 6)}…${a.slice(-4)}` : a);
const usdc = (minor: string | bigint) => fromMinor(minor, 6, 4);
const message = (e: unknown, fallback: string) => (e instanceof Error ? e.message : fallback);

export function VaultPage() {
  const { user } = useSession();
  const userId = user?.id ?? "";
  const { network, demo } = useNetwork();
  const test = network !== "solana";
  const [record, setRecord] = useState<api.VaultRecord | null | undefined>(undefined);
  const [chains, setChains] = useState<api.VaultChainInfo[]>([]);
  const [keystore, setKeystore] = useState<Keystore | null>(null);
  const [balances, setBalances] = useState<api.VaultBalance[] | null>(null);
  const [credits, setCredits] = useState<api.TestCredits | null>(null);
  const [allowances, setAllowances] = useState<api.VaultAllowance[] | null>(null);
  const [panel, setPanel] = useState<Panel>(null);
  const [error, setError] = useState<string | null>(null);
  const [refreshing, setRefreshing] = useState(false);
  const unlocked = useVaultUnlocked();

  const load = useCallback(async () => {
    try {
      const v = await api.getVault();
      setRecord(v.vault);
      setChains(v.chains);
      setError(null);
    } catch (e) {
      setRecord(null);
      setError(message(e, "Couldn't load your vault"));
    }
  }, []);

  useEffect(() => {
    if (demo || !userId) return;
    // eslint-disable-next-line react-hooks/set-state-in-effect -- the vault and this browser's keystore, read once per account
    setKeystore(loadKeystore(userId));
    load();
  }, [demo, userId, load]);

  const refresh = useCallback(
    async (force = false) => {
      if (!record) return;
      setRefreshing(true);
      try {
        const [b, c, a] = await Promise.all([
          api.getVaultBalances(test ? "testnet" : "mainnet", force),
          test ? api.getTestCredits().catch(() => null) : Promise.resolve(null),
          api.getVaultAllowances().catch(() => null),
        ]);
        setBalances(b.balances);
        setCredits(c);
        setAllowances(a?.allowances ?? null);
      } catch (e) {
        setError(message(e, "Couldn't read the balances"));
      } finally {
        setRefreshing(false);
      }
    },
    [record, test],
  );

  useEffect(() => {
    // eslint-disable-next-line react-hooks/set-state-in-effect -- balances follow the vault and the network switch
    refresh();
  }, [refresh]);

  // This browser holds a different wallet than the one recorded: say so rather than mix them.
  const mismatch = !!record && !!keystore && (keystore.addresses.solana !== record.solana_address || keystore.addresses.evm.toLowerCase() !== record.evm_address);
  const here = useMemo(() => vaultChains(test).filter((c) => chains.find((x) => x.id === c.id)?.available), [chains, test]);
  // What agents may spend: on this place to work's Solana, where the server runs allowances.
  const spending = allowances?.find((a) => vaultChain(a.network)?.test === test) ?? null;

  if (demo) {
    return (
      <div className="mx-auto max-w-3xl">
        <PageHeader title="Vault" description="Demo accounts practise with simulated USDC. Sign up to make your own vault." />
      </div>
    );
  }

  async function adopt(ks: Keystore, keys: ReturnType<typeof deriveKeys>) {
    saveKeystore(userId, ks);
    setKeystore(ks);
    const { vault } = await api.registerVault(proveOwnership(userId, keys));
    unlockWith(keys);
    setRecord(vault);
  }

  return (
    <div className="mx-auto max-w-3xl">
      <PageHeader
        title="Vault"
        description="Your money for agents, in a wallet only you hold. The keys are made and kept in this browser; Algebra never has them."
        actions={
          record && keystore && !mismatch ? (
            unlocked ? (
              <Button variant="secondary" onClick={lockVault}>
                <IconUnlock size={14} /> Unlocked · Lock
              </Button>
            ) : (
              <span className={styles.lockedChip}>
                <IconLock size={13} /> Locked
              </span>
            )
          ) : undefined
        }
      />
      {error && <ErrorNote>{error}</ErrorNote>}

      {record === undefined ? (
        <Skeleton className="mt-8 h-64" />
      ) : !record && keystore ? (
        <Reconnect keystore={keystore} onDone={adopt} onStartOver={() => { forgetKeystore(userId); setKeystore(null); }} />
      ) : !record ? (
        <Setup userId={userId} onDone={adopt} />
      ) : (
        <>
          {(!keystore || mismatch) && (
            <div className={styles.notice}>
              <IconShield size={16} />
              <div>
                <strong>{mismatch ? "This browser holds a different wallet" : "This vault was made in another browser"}</strong>
                <p>{mismatch ? "The vault on your account is not the one saved here. Import the vault's phrase or backup to use it here, or make this browser's wallet your vault." : "You can see its balances here. To send from it, import its recovery phrase or backup file."}</p>
              </div>
            </div>
          )}

          <Portfolio test={test} chains={here} balances={balances} credits={credits} vaultPays={!!spending?.active} refreshing={refreshing} onRefresh={() => refresh(true)} />

          <div className={styles.actions}>
            <button type="button" aria-expanded={panel === "deposit"} onClick={() => setPanel(panel === "deposit" ? null : "deposit")}>
              Deposit
            </button>
            <button type="button" aria-expanded={panel === "withdraw"} onClick={() => setPanel(panel === "withdraw" ? null : "withdraw")} disabled={!keystore || mismatch}>
              Withdraw
            </button>
            <button type="button" aria-expanded={panel === "settings"} onClick={() => setPanel(panel === "settings" ? null : "settings")}>
              Settings
            </button>
          </div>

          {panel === "deposit" && <Deposit test={test} chains={here} record={record} serverChains={chains} />}
          {panel === "withdraw" && keystore && !mismatch && <Withdraw chains={here} balances={balances} keystore={keystore} onSent={() => setTimeout(() => refresh(true), 4000)} />}
          {panel === "settings" && (
            <Settings
              userId={userId}
              record={record}
              keystore={mismatch ? null : keystore}
              onReplace={adopt}
              onForget={() => {
                forgetKeystore(userId);
                lockVault();
                setKeystore(null);
              }}
              onRekey={(ks) => {
                saveKeystore(userId, ks);
                setKeystore(ks);
              }}
            />
          )}

          {spending && (
            <AgentSpending
              key={spending.network}
              test={test}
              allowance={spending}
              keystore={keystore && !mismatch ? keystore : null}
              onChanged={(a) => {
                setAllowances((all) => (all ?? []).map((x) => (x.network === a.network ? a : x)));
                setTimeout(() => refresh(true), 3000);
              }}
            />
          )}
        </>
      )}
    </div>
  );
}

function Portfolio({ test, chains, balances, credits, vaultPays, refreshing, onRefresh }: { test: boolean; chains: VaultChain[]; balances: api.VaultBalance[] | null; credits: api.TestCredits | null; vaultPays: boolean; refreshing: boolean; onRefresh: () => void }) {
  const total = (balances ?? []).filter((b) => b.ok).reduce((s, b) => s + BigInt(b.usdc_minor), 0n);
  return (
    <section className={styles.portfolio} aria-label="Portfolio">
      <div className={styles.total}>
        <span>{test ? "Testnet balance" : "Balance"}</span>
        <strong>{balances ? usdc(total) : "—"}<small>USDC</small></strong>
        <button type="button" onClick={onRefresh} disabled={refreshing} aria-label="Refresh balances">
          {refreshing ? <Spinner size={13} /> : <IconRefresh size={14} />}
        </button>
      </div>
      <ul className={styles.chains}>
        {chains.map((c) => {
          const b = balances?.find((x) => x.chain === c.id);
          return (
            <li key={c.id}>
              <ChainMark network={c.id} size={20} />
              <div>
                <strong>{c.name}</strong>
                <small>{b?.ok ? `${fromMinor(b.native_minor, c.native.decimals, 4)} ${c.native.symbol} for fees` : b ? "Couldn't read just now" : "…"}</small>
              </div>
              <span className={styles.amount}>{b?.ok ? usdc(b.usdc_minor) : "—"} <small>USDC</small></span>
            </li>
          );
        })}
        {test && credits && (
          <li className={styles.credits}>
            <span className={styles.creditMark} aria-hidden="true">T</span>
            <div>
              <strong>Testnet credits</strong>
              <small>{vaultPays ? "Not used while agents pay from this vault" : `Spent by agents on Testnet · ${credits.sol_lamports / 1e9} SOL`}</small>
            </div>
            <span className={styles.amount}>{usdc(BigInt(credits.usdc_left_minor))} <small>USDC</small></span>
          </li>
        )}
      </ul>
    </section>
  );
}

function CopyAddress({ value }: { value: string }) {
  const [copied, setCopied] = useState(false);
  return (
    <button
      type="button"
      className={styles.copy}
      onClick={() => {
        navigator.clipboard?.writeText(value).then(() => {
          setCopied(true);
          setTimeout(() => setCopied(false), 1500);
        });
      }}
    >
      <code>{value}</code>
      {copied ? <IconCheck size={14} /> : <IconCopy size={14} />}
    </button>
  );
}

function Deposit({ test, chains, record, serverChains }: { test: boolean; chains: VaultChain[]; record: api.VaultRecord; serverChains: api.VaultChainInfo[] }) {
  const [pick, setPick] = useState(chains[0]?.id ?? "");
  const c = vaultChain(pick);
  const open = serverChains.find((x) => x.id === pick)?.deposits;
  return (
    <section className={styles.sheet} aria-label="Deposit">
      <div className={styles.tabs} role="tablist" aria-label="Chain">
        {chains.map((x) => (
          <button key={x.id} role="tab" aria-selected={pick === x.id} onClick={() => setPick(x.id)}>
            <ChainMark network={x.id} size={14} /> {x.name}
          </button>
        ))}
      </div>
      {c && open ? (
        <>
          <p className={styles.lead}>Send USDC on {c.name}{c.test ? " (test network)" : ""} to your vault. It counts as soon as the chain confirms it.</p>
          <CopyAddress value={c.kind === "solana" ? record.solana_address : checksum(record.evm_address)} />
          <p className={styles.hint}>
            {c.kind === "evm" ? "The same address receives on Base and Arc. " : ""}Only send USDC{c.test ? " on the test network" : ""}: other tokens are not counted.
            {c.faucet && (
              <>
                {" "}Free test USDC: <a href={c.faucet} target="_blank" rel="noopener noreferrer">faucet.circle.com <IconExternal size={11} /></a>
              </>
            )}
          </p>
        </>
      ) : (
        <p className={styles.lead}>{test ? "This chain isn't available on this server yet." : "Mainnet deposits open after an independent security review of the vault. Until then, use Testnet."}</p>
      )}
    </section>
  );
}

/** The server keeps EVM addresses in lower case; people expect the checksum form. */
function checksum(addr: string) {
  try {
    return checksumAddress(addr);
  } catch {
    return addr;
  }
}

/** Password in, keys unlocked: the one place the keystore is opened. */
function useUnlock(keystore: Keystore | null) {
  const [progress, setProgress] = useState<number | null>(null);
  const unlock = useCallback(
    async (password: string) => {
      const have = unlockedKeys();
      if (have) return have;
      if (!keystore) throw new Error("This browser doesn't hold the vault's keys");
      setProgress(0);
      try {
        const phrase = await open(keystore, password, (p) => setProgress(p));
        const keys = deriveKeys(phrase);
        unlockWith(keys);
        return keys;
      } finally {
        setProgress(null);
      }
    },
    [keystore],
  );
  return { unlock, progress };
}

function Progress({ value, label }: { value: number | null; label: string }) {
  if (value === null) return null;
  return (
    <div className={styles.progress} role="progressbar" aria-label={label} aria-valuemin={0} aria-valuemax={100} aria-valuenow={Math.round(value * 100)}>
      <span style={{ transform: `scaleX(${value})` }} />
      <small>{label}</small>
    </div>
  );
}

function Withdraw({ chains, balances, keystore, onSent }: { chains: VaultChain[]; balances: api.VaultBalance[] | null; keystore: Keystore; onSent: () => void }) {
  const unlocked = useVaultUnlocked();
  const { unlock, progress } = useUnlock(keystore);
  const [pick, setPick] = useState(chains[0]?.id ?? "");
  const [to, setTo] = useState("");
  const [amount, setAmount] = useState("");
  const [password, setPassword] = useState("");
  const [review, setReview] = useState(false);
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState<string | null>(null);
  const [sent, setSent] = useState<{ id: string; url?: string } | null>(null);
  const c = vaultChain(pick);
  const bal = balances?.find((b) => b.chain === pick);
  const minor = c ? toMinor(amount, c.usdcDecimals) : null;
  const toOk = c ? (c.kind === "solana" ? isSolanaAddress(to.trim()) : isEvmAddress(to.trim())) : false;
  const enough = minor !== null && bal?.ok ? minor <= BigInt(bal.usdc_minor) : false;
  const noFee = bal?.ok && BigInt(bal.native_minor) === 0n;
  const problem = !c ? "Pick a chain" : !toOk ? `Enter a ${c.kind === "solana" ? "Solana" : "Base / Arc"} address` : minor === null || minor <= 0n ? "Enter an amount" : !enough ? "More than this chain holds" : null;

  async function send() {
    if (!c || minor === null) return;
    setBusy(true);
    setError(null);
    try {
      const keys = await unlock(password);
      const facts = await api.prepareWithdraw({ chain: c.id, to: to.trim(), amount_minor: minor.toString() });
      const { raw, id } = buildWithdrawal(keys, c, to.trim(), minor, facts);
      const { hash } = await api.broadcastVault({ chain: c.id, raw });
      setSent({ id: hash || id, url: c.explorerTx?.(hash || id) });
      setPassword("");
      onSent();
    } catch (e) {
      setError(e instanceof WrongPasswordError ? e.message : message(e, "The withdrawal didn't go through"));
    } finally {
      setBusy(false);
    }
  }

  if (sent) {
    return (
      <section className={styles.sheet} aria-label="Withdrawal sent">
        <p className={styles.done}><IconCheck size={16} /> Sent. The chain confirms it in a few seconds.</p>
        {sent.url ? <a className={styles.txLink} href={sent.url} target="_blank" rel="noopener noreferrer">{short(sent.id)} <IconExternal size={12} /></a> : <code>{short(sent.id)}</code>}
        <div className="mt-4"><Button variant="secondary" onClick={() => { setSent(null); setReview(false); setAmount(""); }}>Send another</Button></div>
      </section>
    );
  }

  return (
    <section className={styles.sheet} aria-label="Withdraw">
      <div className={styles.tabs} role="tablist" aria-label="Chain">
        {chains.map((x) => (
          <button key={x.id} role="tab" aria-selected={pick === x.id} onClick={() => { setPick(x.id); setReview(false); }}>
            <ChainMark network={x.id} size={14} /> {x.name}
          </button>
        ))}
      </div>
      {!review ? (
        <div className="grid gap-4">
          <Field label="To">
            <Input value={to} onChange={(e) => setTo(e.target.value)} placeholder={c?.kind === "solana" ? "Solana address" : "0x… address on Base or Arc"} spellCheck={false} autoComplete="off" className="font-mono" />
          </Field>
          <Field label="Amount (USDC)" hint={bal?.ok ? `${usdc(bal.usdc_minor)} USDC on ${c?.name}` : undefined}>
            <div className="flex gap-2">
              <Input value={amount} onChange={(e) => setAmount(e.target.value)} inputMode="decimal" placeholder="0.00" className="tabular-nums" />
              {bal?.ok && <Button variant="secondary" onClick={() => setAmount(fromMinor(bal.usdc_minor, 6))}>Max</Button>}
            </div>
          </Field>
          {noFee && <p className={styles.hint}>This chain needs a little {c?.native.symbol} in the vault to pay the network fee.</p>}
          <div><Button onClick={() => setReview(true)} disabled={!!problem}>{problem ?? <>Review <IconArrowRight size={14} /></>}</Button></div>
        </div>
      ) : (
        <div className="grid gap-4">
          <dl className={styles.review}>
            <div><dt>Send</dt><dd>{amount} USDC</dd></div>
            <div><dt>On</dt><dd><ChainMark network={c?.id} size={13} /> {c?.name}{c?.test ? " (test network)" : ""}</dd></div>
            <div><dt>To</dt><dd><code>{to.trim()}</code></dd></div>
            <div><dt>Fee</dt><dd>Paid in {c?.native.symbol} from the vault</dd></div>
          </dl>
          <p className={styles.hint}>This browser builds and signs the transaction; Algebra only passes it to the network. A withdrawal can&apos;t be undone.</p>
          {!unlocked && (
            <Field label="Wallet password">
              <Input type="password" value={password} onChange={(e) => setPassword(e.target.value)} autoComplete="current-password" />
            </Field>
          )}
          <Progress value={progress} label="Opening the vault" />
          {error && <ErrorNote>{error}</ErrorNote>}
          <div className="flex gap-2">
            <Button variant="secondary" onClick={() => setReview(false)} disabled={busy}>Back</Button>
            <Button onClick={send} disabled={busy || (!unlocked && password.length < MIN_PASSWORD)}>{busy && <Spinner size={14} />} Sign and send</Button>
          </div>
        </div>
      )}
    </section>
  );
}

const PRESETS = ["1", "5", "10", "25"];

/**
 * What agents may spend from the vault: a limit the token program enforces
 * on Solana, set and stopped by a transaction this browser signs. Every Spend
 * Pass draws from it; none can take more than is left of it.
 */
function AgentSpending({ test, allowance: a, keystore, onChanged }: { test: boolean; allowance: api.VaultAllowance; keystore: Keystore | null; onChanged: (a: api.VaultAllowance) => void }) {
  const unlocked = useVaultUnlocked();
  const { unlock, progress } = useUnlock(keystore);
  const [mode, setMode] = useState<null | "set" | "stop">(null);
  const [amount, setAmount] = useState("5");
  const [password, setPassword] = useState("");
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState<string | null>(null);
  const [done, setDone] = useState<{ id: string; pending: boolean; stopped: boolean } | null>(null);
  const c = vaultChain(a.network);
  const minor = toMinor(amount, 6);
  const left = a.active ? a.left_minor : 0;
  const share = a.active && a.approved_minor > 0 ? Math.min(1, left / a.approved_minor) : 0;
  const problem = mode === "set" ? (minor === null || minor <= 0n ? "Enter an amount" : minor > BigInt(a.max_minor) ? `At most ${usdc(BigInt(a.max_minor))} USDC` : null) : null;

  async function sign() {
    if (!keystore) return;
    setBusy(true);
    setError(null);
    try {
      const keys = await unlock(password);
      const facts = await api.prepareAllowance(a.network);
      const transaction = buildAllowance(keys, facts, mode === "stop" ? null : minor);
      const res = await api.submitAllowance({ network: a.network, transaction });
      setDone({ id: res.signature, pending: res.pending, stopped: mode === "stop" });
      setPassword("");
      setMode(null);
      onChanged(res.allowance);
    } catch (e) {
      setError(e instanceof WrongPasswordError ? e.message : message(e, "That didn't go through; nothing changed"));
    } finally {
      setBusy(false);
    }
  }

  return (
    <section className={styles.spending} aria-label="Agent spending">
      <div className={styles.spendingHead}>
        <ChainMark network={a.network} size={20} />
        <div>
          <strong>Agents can spend</strong>
          <small>{a.active ? `From this vault, capped on ${c?.name ?? "Solana"}` : test ? "Off: agents use your Testnet credits" : "Off: agents can't spend from this vault"}</small>
        </div>
        <span className={styles.amount}>
          {a.active ? (
            <>
              {a.read ? usdc(BigInt(left)) : "—"} <small>of {usdc(BigInt(a.approved_minor))} USDC</small>
            </>
          ) : (
            <small>Off</small>
          )}
        </span>
      </div>
      {a.active && (
        <div className={styles.spendingBar} role="meter" aria-label="Limit left" aria-valuemin={0} aria-valuemax={a.approved_minor} aria-valuenow={left} data-level={share <= 0 ? "out" : share < 0.2 ? "low" : "ok"}>
          <span style={{ transform: `scaleX(${share})` }} />
        </div>
      )}

      {done && !mode && (
        <p className={styles.spendingDone}>
          <IconCheck size={14} /> {done.stopped ? "Stopped." : "Set."} {done.pending ? "The network is still confirming it." : "Confirmed on chain."}{" "}
          {c?.explorerTx && (
            <a className={styles.txLink} href={c.explorerTx(done.id)} target="_blank" rel="noopener noreferrer">
              {short(done.id)} <IconExternal size={11} />
            </a>
          )}
        </p>
      )}

      {!mode ? (
        <div className={styles.spendingActions}>
          <button type="button" onClick={() => { setMode("set"); setDone(null); setError(null); }} disabled={!keystore || !a.has_token_account}>
            {a.active ? "Change limit" : "Let agents spend"}
          </button>
          {a.active && (
            <button type="button" onClick={() => { setMode("stop"); setDone(null); setError(null); }} disabled={!keystore}>
              Stop
            </button>
          )}
          {a.read && !a.has_token_account && <p className={styles.hint}>Deposit USDC on {c?.name ?? "Solana"} first: the limit is set on the vault&apos;s USDC account.</p>}
          {!keystore && <p className={styles.hint}>Import this vault in this browser to change what agents can spend.</p>}
        </div>
      ) : (
        <div className={styles.spendingSheet}>
          {mode === "set" ? (
            <>
              <Field label="Agents may spend up to (USDC)" hint="This replaces the limit before it. Every Spend Pass draws from it.">
                <Input value={amount} onChange={(e) => setAmount(e.target.value)} inputMode="decimal" className="tabular-nums" autoFocus />
              </Field>
              <div className={styles.presets} role="group" aria-label="Quick amounts">
                {PRESETS.map((p) => (
                  <button key={p} type="button" aria-pressed={amount === p} onClick={() => setAmount(p)}>
                    {p}
                  </button>
                ))}
              </div>
            </>
          ) : (
            <p className={styles.lead}>Agents stop paying from this vault.{test ? " On Testnet they use your test credits again." : ""} Payments already made stay made.</p>
          )}
          <dl className={styles.review}>
            {mode === "set" && (
              <div>
                <dt>Limit</dt>
                <dd>{minor !== null && minor > 0n ? usdc(minor) : "—"} USDC</dd>
              </div>
            )}
            <div>
              <dt>Spender</dt>
              <dd>
                Algebra&apos;s payer <code>{short(a.delegate)}</code>
              </dd>
            </div>
            <div>
              <dt>On</dt>
              <dd>
                <ChainMark network={a.network} size={13} /> {c?.name}
                {c?.test ? " (test network)" : ""}
              </dd>
            </div>
            <div>
              <dt>Fee</dt>
              <dd>Paid by Algebra</dd>
            </div>
          </dl>
          {mode === "set" && (
            <p className={styles.hint}>
              Solana&apos;s token program holds the line: nothing, Algebra included, can move more than this from your vault with it. A refund of a failed payment returns the USDC but not the limit.
              {a.other_delegate ? ` It also replaces the spender ${short(a.other_delegate)} you allowed elsewhere.` : ""}
            </p>
          )}
          {!unlocked && (
            <Field label="Wallet password">
              <Input type="password" value={password} onChange={(e) => setPassword(e.target.value)} autoComplete="current-password" />
            </Field>
          )}
          <Progress value={progress} label="Opening the vault" />
          {error && <ErrorNote>{error}</ErrorNote>}
          <div className="flex gap-2">
            <Button variant="secondary" onClick={() => setMode(null)} disabled={busy}>
              Back
            </Button>
            <Button onClick={sign} disabled={busy || !!problem || (!unlocked && password.length < MIN_PASSWORD)}>
              {busy && <Spinner size={14} />} {problem ?? (mode === "stop" ? "Sign and stop" : "Sign and set")}
            </Button>
          </div>
        </div>
      )}
    </section>
  );
}

function download(ks: Keystore) {
  const url = URL.createObjectURL(backupFile(ks));
  const a = document.createElement("a");
  a.href = url;
  a.download = `algebra-vault-backup-${ks.addresses.solana.slice(0, 6)}.json`;
  a.click();
  setTimeout(() => URL.revokeObjectURL(url), 2000);
}

function Settings({ userId, record, keystore, onReplace, onForget, onRekey }: { userId: string; record: api.VaultRecord; keystore: Keystore | null; onReplace: (ks: Keystore, keys: ReturnType<typeof deriveKeys>) => Promise<void>; onForget: () => void; onRekey: (ks: Keystore) => void }) {
  const [phrase, setPhrase] = useState<string | null>(null);
  const [password, setPassword] = useState("");
  const [next, setNext] = useState("");
  const [mode, setMode] = useState<null | "phrase" | "password" | "import">(null);
  const [busy, setBusy] = useState(false);
  const [progress, setProgress] = useState<number | null>(null);
  const [error, setError] = useState<string | null>(null);
  const [note, setNote] = useState<string | null>(null);

  async function reveal() {
    if (!keystore) return;
    setBusy(true);
    setError(null);
    try {
      setPhrase(await open(keystore, password, setProgress));
      setPassword("");
    } catch (e) {
      setError(message(e, "Couldn't open the vault"));
    } finally {
      setBusy(false);
      setProgress(null);
    }
  }

  async function rekey() {
    if (!keystore) return;
    setBusy(true);
    setError(null);
    try {
      const p = await open(keystore, password, (x) => setProgress(x / 2));
      const ks = await seal(p, next, (x) => setProgress(0.5 + x / 2));
      onRekey(ks);
      setNote("Password changed. Download a fresh backup: the old one still opens with the old password.");
      setMode(null);
      setPassword("");
      setNext("");
    } catch (e) {
      setError(message(e, "Couldn't change the password"));
    } finally {
      setBusy(false);
      setProgress(null);
    }
  }

  return (
    <section className={styles.sheet} aria-label="Vault settings">
      <dl className={styles.review}>
        <div><dt><ChainMark network="solana" size={13} /> Solana</dt><dd><CopyAddress value={record.solana_address} /></dd></div>
        <div><dt><ChainMark network="base" size={13} /> Base &amp; Arc</dt><dd><CopyAddress value={checksum(record.evm_address)} /></dd></div>
      </dl>
      <p className={styles.hint}>You can export this vault at any time: its recovery phrase imports into Phantom, Solflare and any EVM wallet (MetaMask, Rabby), and its backup file imports here.</p>

      {note && <p className={styles.done}><IconCheck size={15} /> {note}</p>}
      {error && <ErrorNote>{error}</ErrorNote>}

      {phrase ? (
        <div className="grid gap-3">
          <Words phrase={phrase} />
          <p className={styles.hint}>Anyone with these words owns this vault. Write them down; never type them into a website.</p>
          <div><Button variant="secondary" onClick={() => setPhrase(null)}>Hide</Button></div>
        </div>
      ) : mode === "phrase" || mode === "password" ? (
        <div className="grid gap-3">
          <Field label={mode === "phrase" ? "Wallet password" : "Current password"}>
            <Input type="password" value={password} onChange={(e) => setPassword(e.target.value)} autoComplete="current-password" />
          </Field>
          {mode === "password" && (
            <Field label="New password" hint={`At least ${MIN_PASSWORD} characters`}>
              <Input type="password" value={next} onChange={(e) => setNext(e.target.value)} autoComplete="new-password" />
            </Field>
          )}
          <Progress value={progress} label="Working" />
          <div className="flex gap-2">
            <Button variant="secondary" onClick={() => setMode(null)} disabled={busy}>Cancel</Button>
            <Button onClick={mode === "phrase" ? reveal : rekey} disabled={busy || password.length < MIN_PASSWORD || (mode === "password" && next.length < MIN_PASSWORD)}>
              {busy && <Spinner size={14} />} {mode === "phrase" ? "Show the phrase" : "Change password"}
            </Button>
          </div>
        </div>
      ) : mode === "import" ? (
        <Setup userId={userId} onDone={async (ks, keys) => { await onReplace(ks, keys); setMode(null); setNote("This browser's wallet is now your vault."); }} importOnly />
      ) : (
        <div className={styles.settingRows}>
          {keystore && <button type="button" onClick={() => setMode("phrase")}>Show recovery phrase</button>}
          {keystore && <button type="button" onClick={() => download(keystore)}>Download encrypted backup</button>}
          {keystore && <button type="button" onClick={() => setMode("password")}>Change wallet password</button>}
          <button type="button" onClick={() => setMode("import")}>Import a phrase or backup</button>
          {keystore && (
            <button type="button" className={styles.danger} onClick={() => { if (window.confirm("Remove the vault from this browser? Your money stays on-chain; you will need the phrase or backup to send from here again.")) onForget(); }}>
              Remove from this browser
            </button>
          )}
        </div>
      )}
    </section>
  );
}

/** A wallet saved in this browser whose recording on the account didn't finish: unlock it to finish. */
function Reconnect({ keystore, onDone, onStartOver }: { keystore: Keystore; onDone: (ks: Keystore, keys: ReturnType<typeof deriveKeys>) => Promise<void>; onStartOver: () => void }) {
  const [password, setPassword] = useState("");
  const [busy, setBusy] = useState(false);
  const [progress, setProgress] = useState<number | null>(null);
  const [error, setError] = useState<string | null>(null);
  async function go() {
    setBusy(true);
    setError(null);
    try {
      const words = await open(keystore, password, setProgress);
      await onDone(keystore, deriveKeys(words));
    } catch (e) {
      setError(message(e, "Couldn't finish setting up the vault"));
    } finally {
      setBusy(false);
      setProgress(null);
    }
  }
  return (
    <section className={styles.sheet} aria-label="Finish setting up your vault">
      <p className={styles.lead}>This browser holds a vault that isn&apos;t on your account yet. Enter its password to finish setting it up.</p>
      <Field label="Wallet password">
        <Input type="password" value={password} onChange={(e) => setPassword(e.target.value)} autoComplete="current-password" />
      </Field>
      <Progress value={progress} label="Opening the vault" />
      {error && <ErrorNote>{error}</ErrorNote>}
      <div className="flex flex-wrap gap-2">
        <Button onClick={go} disabled={busy || password.length < MIN_PASSWORD}>{busy && <Spinner size={14} />} Finish setting up</Button>
        <Button variant="secondary" onClick={() => { if (window.confirm("Forget the wallet saved in this browser and start again? Keep its phrase if it holds money.")) onStartOver(); }} disabled={busy}>Start again</Button>
      </div>
    </section>
  );
}

function Words({ phrase }: { phrase: string }) {
  return (
    <ol className={styles.words}>
      {phrase.split(" ").map((w, i) => (
        <li key={i}><small>{i + 1}</small>{w}</li>
      ))}
    </ol>
  );
}

/** Two word positions to ask back, different each time. */
function pickTwo(): [number, number] {
  const a = crypto.getRandomValues(new Uint8Array(1))[0] % 12;
  let b = crypto.getRandomValues(new Uint8Array(1))[0] % 12;
  if (b === a) b = (a + 5) % 12;
  return a < b ? [a, b] : [b, a];
}

function Setup({ userId, onDone, importOnly = false }: { userId: string; onDone: (ks: Keystore, keys: ReturnType<typeof deriveKeys>) => Promise<void>; importOnly?: boolean }) {
  const [mode, setMode] = useState<"choose" | "create" | "import">(importOnly ? "import" : "choose");
  const [step, setStep] = useState<"password" | "words" | "check">("password");
  const [password, setPassword] = useState("");
  const [again, setAgain] = useState("");
  const [phrase, setPhrase] = useState("");
  const [wrote, setWrote] = useState(false);
  const [ask] = useState(pickTwo);
  const [answers, setAnswers] = useState(["", ""]);
  const [backup, setBackup] = useState<Keystore | null>(null);
  const [busy, setBusy] = useState(false);
  const [progress, setProgress] = useState<number | null>(null);
  const [error, setError] = useState<string | null>(null);
  void userId;

  const pwProblem = password.length < MIN_PASSWORD ? `At least ${MIN_PASSWORD} characters` : !backup && password !== again ? "The passwords don't match" : null;

  async function finish(words: string) {
    setBusy(true);
    setError(null);
    try {
      const ks = await seal(words, password, setProgress);
      const keys = deriveKeys(words);
      await onDone(ks, keys);
    } catch (e) {
      setError(message(e, "Couldn't make the vault"));
    } finally {
      setBusy(false);
      setProgress(null);
    }
  }

  async function importBackup() {
    if (!backup) return;
    setBusy(true);
    setError(null);
    try {
      const words = await open(backup, password, setProgress);
      await onDone(backup, deriveKeys(words));
    } catch (e) {
      setError(message(e, "Couldn't open that backup"));
    } finally {
      setBusy(false);
      setProgress(null);
    }
  }

  if (mode === "choose") {
    return (
      <section className={styles.setup} aria-label="Set up your vault">
        <div className={styles.setupArt} aria-hidden="true">
          <ChainMark network="solana" size={22} />
          <ChainMark network="base" size={22} />
          <ChainMark network="arc" size={22} />
        </div>
        <h2>One vault for Solana, Base and Arc</h2>
        <p>Your agents spend from it only within the limits you set. It is a wallet only you hold: made in this browser, locked with your own password, recoverable with 12 words.</p>
        <div className="mt-6 flex flex-wrap justify-center gap-2">
          <Button onClick={() => { setPhrase(newMnemonic()); setMode("create"); }}>Create my vault</Button>
          <Button variant="secondary" onClick={() => setMode("import")}>I have a phrase or backup</Button>
        </div>
      </section>
    );
  }

  if (mode === "create") {
    const words = phrase.split(" ");
    const checked = answers[0].trim().toLowerCase() === words[ask[0]] && answers[1].trim().toLowerCase() === words[ask[1]];
    return (
      <section className={styles.sheet} aria-label="Create your vault">
        <ol className={styles.steps} aria-label="Steps">
          {["Password", "Recovery phrase", "Check"].map((s, i) => (
            <li key={s} data-on={["password", "words", "check"].indexOf(step) >= i || undefined}>{s}</li>
          ))}
        </ol>
        {step === "password" && (
          <div className="grid gap-4">
            <p className={styles.lead}>This password opens the vault in this browser. It is not your sign-in, and Algebra never sees it.</p>
            <Field label="Wallet password" hint={`At least ${MIN_PASSWORD} characters. A short sentence works well.`}>
              <Input type="password" value={password} onChange={(e) => setPassword(e.target.value)} autoComplete="new-password" />
            </Field>
            <Field label="Same password again">
              <Input type="password" value={again} onChange={(e) => setAgain(e.target.value)} autoComplete="new-password" />
            </Field>
            <div><Button onClick={() => setStep("words")} disabled={!!pwProblem}>{pwProblem ?? <>Next <IconArrowRight size={14} /></>}</Button></div>
          </div>
        )}
        {step === "words" && (
          <div className="grid gap-4">
            <p className={styles.lead}>These 12 words are your vault. Write them down in order and keep them offline. With them you can recover the vault anywhere; without them and your password, nobody can.</p>
            <Words phrase={phrase} />
            <label className={styles.check}>
              <input type="checkbox" checked={wrote} onChange={(e) => setWrote(e.target.checked)} /> I wrote these 12 words down
            </label>
            <div className="flex gap-2">
              <Button variant="secondary" onClick={() => setStep("password")}>Back</Button>
              <Button onClick={() => setStep("check")} disabled={!wrote}>Next <IconArrowRight size={14} /></Button>
            </div>
          </div>
        )}
        {step === "check" && (
          <div className="grid gap-4">
            <p className={styles.lead}>Type two of the words, to be sure they were written down.</p>
            <div className="grid grid-cols-2 gap-3">
              {ask.map((pos, i) => (
                <Field key={pos} label={`Word ${pos + 1}`}>
                  <Input value={answers[i]} onChange={(e) => setAnswers((a) => a.map((x, j) => (j === i ? e.target.value : x)))} autoComplete="off" spellCheck={false} />
                </Field>
              ))}
            </div>
            <Progress value={progress} label="Locking the vault with your password" />
            {error && <ErrorNote>{error}</ErrorNote>}
            <div className="flex gap-2">
              <Button variant="secondary" onClick={() => setStep("words")} disabled={busy}>Back</Button>
              <Button onClick={() => finish(phrase)} disabled={!checked || busy}>{busy && <Spinner size={14} />} Create my vault</Button>
            </div>
          </div>
        )}
      </section>
    );
  }

  const phraseOk = isMnemonic(phrase);
  return (
    <section className={styles.sheet} aria-label="Import a vault">
      <p className={styles.lead}>Import the 12 or 24 words of a vault (or any Phantom or EVM wallet&apos;s phrase), or the encrypted backup file you downloaded.</p>
      {!backup ? (
        <div className="grid gap-4">
          <Field label="Recovery phrase">
            <textarea className={styles.phraseInput} value={phrase} onChange={(e) => setPhrase(e.target.value)} rows={3} spellCheck={false} autoComplete="off" placeholder="word word word …" />
          </Field>
          <label className={styles.file}>
            Or choose a backup file
            <input
              type="file"
              accept="application/json,.json"
              onChange={async (e) => {
                const f = e.target.files?.[0];
                if (!f) return;
                try {
                  const v = JSON.parse(await f.text()) as unknown;
                  if (!isKeystore(v)) throw new Error("That file isn't an Algebra vault backup");
                  setBackup(v);
                  setError(null);
                } catch (err) {
                  setError(message(err, "Couldn't read that file"));
                }
              }}
            />
          </label>
          {phrase.trim() && (
            <>
              <Field label="New wallet password" hint={`At least ${MIN_PASSWORD} characters`}>
                <Input type="password" value={password} onChange={(e) => setPassword(e.target.value)} autoComplete="new-password" />
              </Field>
              <Field label="Same password again">
                <Input type="password" value={again} onChange={(e) => setAgain(e.target.value)} autoComplete="new-password" />
              </Field>
            </>
          )}
        </div>
      ) : (
        <div className="grid gap-4">
          <p className={styles.hint}>Backup of vault {short(backup.addresses.solana)} · {short(backup.addresses.evm)}</p>
          <Field label="The backup's password">
            <Input type="password" value={password} onChange={(e) => setPassword(e.target.value)} autoComplete="current-password" />
          </Field>
        </div>
      )}
      <Progress value={progress} label={backup ? "Opening the backup" : "Locking the vault with your password"} />
      {error && <ErrorNote>{error}</ErrorNote>}
      <div className="flex flex-wrap gap-2">
        {!importOnly && <Button variant="secondary" onClick={() => { setMode("choose"); setBackup(null); setPhrase(""); }} disabled={busy}>Back</Button>}
        {backup ? (
          <Button onClick={importBackup} disabled={busy || password.length < MIN_PASSWORD}>{busy && <Spinner size={14} />} Import the backup</Button>
        ) : (
          <Button onClick={() => finish(normalizeMnemonic(phrase))} disabled={busy || !phraseOk || !!pwProblem}>
            {busy && <Spinner size={14} />} {!phrase.trim() ? "Enter a phrase" : !phraseOk ? "That isn't a valid phrase" : pwProblem ?? "Import"}
          </Button>
        )}
      </div>
    </section>
  );
}
