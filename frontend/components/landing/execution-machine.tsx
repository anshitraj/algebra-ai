"use client";

import Link from "next/link";
import { useEffect, useId, useRef, useState, type ReactNode } from "react";
import { Logo } from "@/components/logo";
import { IconArrowRight } from "@/components/icons";
import styles from "./execution-machine.module.css";

const stations = [
  { id: "agent", name: "Your agent", detail: "Claude, Cursor, the OpenAI Agents SDK, or your own agent asks for a capability through REST or MCP. It supplies a maximum spend, never a wallet key." },
  { id: "algebra", name: "Spend Pass", detail: "Algebra compares eligible quotes and enforces your Spend Pass on the server. Budget, provider rules and per-call limits must pass before a payment is authorized." },
  { id: "provider", name: "API providers", detail: "Choose a capability, not a vendor integration. Algebra finds an eligible provider such as Birdeye for token intelligence, Exa for search, or Quicknode for RPC." },
  { id: "settlement", name: "USDC payment", detail: "Algebra authorizes the payment within your limits and settles in USDC on Solana. Your agent never receives the signing key." },
  { id: "receipt", name: "signed receipt", detail: "Your agent receives the result and a signed receipt. Trace the route, policy decision and settlement, then verify the proof independently." },
  { id: "vault", name: "Your vault", detail: "Your vault is your own wallet: Algebra never holds your keys. You give the Spend Pass a capped USDC limit, and the Solana program checks it before releasing the exact amount a payment needs. Until then the funds stay in your wallet, and you can freeze the pass or stop the limit at any time." },
] as const;
type StationId = typeof stations[number]["id"];

function AlgebraMark({ x, y, size = 40 }: { x: number; y: number; size?: number }) {
  return <g transform={`translate(${x} ${y}) scale(${size / 40})`} className={styles.algebraMark}><path d="M18 5H8v24h10V19H8 M22 35h10V11H22v10h10" fill="none" stroke="currentColor" strokeWidth="4" strokeLinejoin="round" /></g>;
}

function Module({ id, x, y, width, height, selected, onSelect, children }: {
  id: StationId; x: number; y: number; width: number; height: number; selected: boolean; onSelect: () => void; children: ReactNode;
}) {
  return (
    <g data-station={id} className={`${styles.module} ${selected ? styles.selected : ""}`} onClick={onSelect}>
      <ellipse cx={x + width / 2 + 10} cy={y + height + 13} rx={width * .62} ry="18" className={styles.moduleShadow} />
      <polygon points={`${x},${y} ${x + 15},${y - 12} ${x + width + 15},${y - 12} ${x + width},${y}`} className={styles.moduleTop} />
      <polygon points={`${x + width},${y} ${x + width + 15},${y - 12} ${x + width + 15},${y + height - 12} ${x + width},${y + height}`} className={styles.moduleSide} />
      <rect x={x} y={y} width={width} height={height} rx="6" className={styles.moduleFace} />
      <path d={`M${x + 12} ${y + height - 7}H${x + width - 12}`} className={styles.moduleEdge} />
      <g transform={`translate(${x} ${y})`}>{children}</g>
    </g>
  );
}

/** Rendered in the initial HTML; no scene bundle, font wait or GPU initialization. */
export function ExecutionMachine() {
  const [selected, setSelected] = useState<StationId>("algebra");
  const [paused, setPaused] = useState(false);
  const viewport = useRef<HTMLDivElement>(null);
  const uniqueId = useId();
  const active = stations.find((station) => station.id === selected)!;
  const plateId = `${uniqueId}-plate`;
  const solanaId = `${uniqueId}-solana`;
  function focusStation(id: StationId) {
    setSelected(id);
    const element = viewport.current;
    if (!element || element.scrollWidth <= element.clientWidth) return;
    const stationElement = element.querySelector(`[data-station="${id}"]`);
    if (!stationElement) return;
    const bounds = stationElement.getBoundingClientRect();
    const left = element.scrollLeft + bounds.left - element.getBoundingClientRect().left - (element.clientWidth - bounds.width) / 2;
    element.scrollTo({ left, behavior: matchMedia('(prefers-reduced-motion: reduce)').matches ? 'auto' : 'smooth' });
  }
  useEffect(() => {
    const element = viewport.current;
    if (element && element.scrollWidth > element.clientWidth) element.scrollLeft = element.scrollWidth / 2 - element.clientWidth / 2;
  }, []);

  return (
    <section id="execution-engine" aria-labelledby="execution-heading" className={`landing-container ${styles.section}`}>
      <div className="section-heading">
        <div><span className="eyebrow"><i /> THE ENGINE BEHIND THE OUTCOME</span><h2 id="execution-heading">Your agent asks.<br /><em>Algebra connects the dots.</em></h2></div>
        <p>One request. The right API. A payment within your rules. See how Algebra brings the pieces together.</p>
      </div>
      <div className={`${styles.frame} ${paused ? styles.paused : ""}`}>
        <div className={styles.topbar}>
          <div className={styles.identity}><Logo size={26} /><span><strong>algebra</strong><small>THE AGENT ECONOMY, CONNECTED</small></span></div>
          <span className={styles.example}><i /> ILLUSTRATIVE EXECUTION</span>
        </div>
        <span className={styles.mobileHint}>Swipe to explore · or choose a stage below <IconArrowRight size={12} /></span>
        <div className={styles.diagramViewport} ref={viewport}>
          <svg viewBox="0 40 1120 615" className={styles.diagram} role="img" aria-label="Algebra execution diagram">
            <title>Algebra execution diagram</title>
            <desc>You give the Spend Pass a capped USDC limit from your own wallet. Agents connect through REST or MCP. Algebra routes requests, checks the Spend Pass, and the program checks the vault’s limits before releasing funds for payment. A result and signed receipt return to your agent. Select a stage using the buttons below.</desc>
            <defs>
              <linearGradient id={plateId} x1="0" y1="0" x2=".8" y2="1"><stop stopColor="#353c32" /><stop offset="1" stopColor="#20251f" /></linearGradient>
              <linearGradient id={solanaId}><stop stopColor="#98dbc0" /><stop offset="1" stopColor="#b6abe3" /></linearGradient>
              <pattern id={`${uniqueId}-grid`} width="32" height="32" patternUnits="userSpaceOnUse"><path d="M32 0H0V32" fill="none" stroke="#9aa78b" strokeOpacity=".045" /></pattern>
            </defs>
            <rect width="1120" height="650" fill={`url(#${uniqueId}-grid)`} />
            <ellipse cx="570" cy="511" rx="442" ry="105" className={styles.boardShadow} />
            <path d="M90 280 735 145 1040 360 930 560 395 640 80 430Z" transform="translate(0 15)" className={styles.plateSide} />
            <path d="M90 280 735 145 1040 360 930 560 395 640 80 430Z" fill={`url(#${plateId})`} className={styles.plate} />
            <path d="m112 300 619-130 288 196-104 172-518 79-294-192Z" className={styles.plateInset} />
            {[[107, 312], [731, 169], [1011, 367], [908, 537], [398, 613], [107, 418]].map(([x, y]) => <g key={`${x}-${y}`}><ellipse cx={x} cy={y} rx="5" ry="3" className={styles.screw} /><path d={`M${x - 2} ${y}h4`} stroke="#171b15" /></g>)}
            <g className={styles.traces}>
              <path d="M333 316H387L450 283" /><path d="M664 283H718L779 250" /><path d="M875 350V388L873 417" /><path d="M786 491 706 533H623" /><path d="M432 512H343L246 428V383" />
              <path d="M572 202V215M663 149H708L751 387H865V417" />
              <path d="M332 325H391L450 292M664 292H721L779 259M885 350V388L883 417M786 501 708 543H623" className={styles.secondaryTrace} />
            </g>
            <g className={styles.signals}><path d="M333 316H387L450 283" /><path d="M664 283H718L779 250" /><path d="M875 350V388L873 417" /><path d="M786 491 706 533H623" /><path d="M432 512H343L246 428V383" /><path d="M663 149H708L751 387H865V417" /></g>
            <text x="353" y="302" className={styles.wireLabel}>REST / MCP</text><text x="686" y="268" className={styles.wireLabel}>BEST QUOTE</text><text x="899" y="391" className={styles.wireLabel}>AUTHORIZE</text><text x="647" y="520" className={styles.wireLabel}>RESULT + PROOF</text>
            <text x="744" y="377" className={styles.wireLabel}>LIMITED USDC PULL</text>

            <Module id="vault" x={481} y={78} width={182} height={124} selected={selected === "vault"} onSelect={() => focusStation("vault")}>
              <text x="16" y="24" className={styles.number}>YOUR LIMIT / ON-CHAIN</text>
              <g className={styles.vaultIcon}><rect x="17" y="38" width="44" height="47" rx="4" /><circle cx="39" cy="61" r="13" /><path d="M39 51v20M29 61h20M21 43v8m0 19v9" /><circle cx="39" cy="61" r="3" /></g>
              <text x="73" y="55" className={styles.moduleTitle}>Your vault</text><text x="73" y="72" className={styles.moduleMeta}>Held in your wallet</text>
              <g transform="translate(18 98) scale(.7)" fill={`url(#${solanaId})`}><path d="M3 0h23l-3 4H0zM0 7h23l3 4H3zM3 14h23l-3 4H0z" /></g><text x="48" y="109" className={styles.moduleMeta}>Program-checked limits</text>
            </Module>

            <Module id="agent" x={153} y={235} width={180} height={148} selected={selected === "agent"} onSelect={() => focusStation("agent")}>
              <text x="16" y="24" className={styles.number}>01 / INPUT</text><text x="16" y="49" className={styles.moduleTitle}>Your agent</text>
              {["claude", "openai", "cursor"].map((name, index) => <g key={name}><rect x={16 + index * 48} y="62" width="36" height="36" rx="8" className={styles.logoTile} /><image href={`/connectors/${name}.svg`} x={24 + index * 48} y="70" width="20" height="20" /></g>)}
              <text x="16" y="119" className={styles.code}>token.price</text><text x="16" y="135" className={styles.moduleMeta}>MAX 0.01 USDC</text>
            </Module>
            <Module id="algebra" x={450} y={215} width={214} height={186} selected={selected === "algebra"} onSelect={() => focusStation("algebra")}>
              <text x="18" y="24" className={styles.number}>02 / CONTROL LAYER</text><AlgebraMark x={18} y={39} size={42} /><text x="73" y="71" className={styles.coreWordmark}>algebra</text>
              <path d="M18 88H196" className={styles.divider} /><text x="18" y="110" className={styles.moduleTitle}>Spend Pass</text>
              <g className={styles.policyChecks}><path d="m18 125 3 3 5-6m-8 21 3 3 5-6" /><text x="35" y="129">Budget &amp; provider rules</text><text x="35" y="147">Server-side authorization</text></g>
              <rect x="18" y="158" width="178" height="17" rx="3" className={styles.allowedBadge} /><text x="107" y="170" textAnchor="middle" className={styles.allowedText}>WITHIN YOUR LIMITS</text>
            </Module>
            <Module id="provider" x={779} y={160} width={194} height={190} selected={selected === "provider"} onSelect={() => focusStation("provider")}>
              <text x="16" y="24" className={styles.number}>03 / SMART ROUTING</text><text x="16" y="49" className={styles.moduleTitle}>API providers</text>
              {[
                { name: "Birdeye", file: "birdeye", detail: "Token intelligence" }, { name: "Exa", file: "exa", detail: "Web search" }, { name: "Quicknode", file: "quicknode", detail: "Blockchain RPC" },
              ].map((provider, index) => <g key={provider.name} transform={`translate(16 ${63 + index * 38})`}><rect width="28" height="28" rx="6" className={styles.logoTile} /><image href={`/providers/${provider.file}.png`} x="4" y="4" width="20" height="20" /><text x="38" y="11" className={styles.providerName}>{provider.name}</text><text x="38" y="25" className={styles.moduleMeta}>{provider.detail}</text>{index === 0 && <circle cx="158" cy="11" r="3" className={styles.statusLight} />}</g>)}
              <text x="16" y="178" className={styles.quote}>EXAMPLE QUOTE · 0.003 USDC</text>
            </Module>
            <Module id="settlement" x={786} y={417} width={187} height={124} selected={selected === "settlement"} onSelect={() => focusStation("settlement")}>
              <text x="16" y="23" className={styles.number}>04 / PAYMENT</text><circle cx="35" cy="58" r="18" className={styles.usdcCoin} /><text x="35" y="65" textAnchor="middle" className={styles.dollar}>$</text>
              <text x="65" y="55" className={styles.moduleTitle}>USDC</text><text x="65" y="72" className={styles.moduleMeta}>Controlled spending</text>
              <g transform="translate(17 93)" fill={`url(#${solanaId})`}><path d="M3 0h23l-3 4H0zM0 7h23l3 4H3zM3 14h23l-3 4H0z" /></g><text x="54" y="106" className={styles.providerName}>Settled on Solana</text>
            </Module>
            <Module id="receipt" x={432} y={449} width={191} height={124} selected={selected === "receipt"} onSelect={() => focusStation("receipt")}>
              <text x="16" y="23" className={styles.number}>05 / OUTPUT</text><path d="M18 39h25l6 6v38H18Zm25 0v8h6M24 55h18m-18 8h18m-18 8h10" className={styles.receiptIcon} />
              <text x="63" y="56" className={styles.moduleTitle}>Result + receipt</text><text x="63" y="74" className={styles.moduleMeta}>Signed. Verifiable.</text><path d="m20 100 3 3 5-6" className={styles.check} /><text x="37" y="103" className={styles.quote}>EVERY OUTCOME ACCOUNTED FOR</text>
            </Module>
            <g transform="translate(146 440) rotate(28)"><AlgebraMark x={0} y={0} size={23} /><text x="32" y="17" className={styles.engraving}>ALGEBRA / EXECUTION LAYER</text></g>
          </svg>
        </div>
        <div className={styles.frameFooter}><span>REQUEST → ROUTE → AUTHORIZE → EXECUTE → VERIFY</span><button type="button" onClick={() => setPaused((value) => !value)} aria-label={paused ? "Resume flow animation" : "Pause flow animation"} aria-pressed={paused}>{paused ? "▶" : "Ⅱ"}<span>{paused ? "Resume flow" : "Pause flow"}</span></button></div>
      </div>
      <div className={styles.explorer}>
        <div className={styles.stationButtons} role="group" aria-label="Explore Algebra execution stages">{stations.map((station, index) => <button key={station.id} type="button" aria-label={`Explore ${station.name}`} aria-pressed={selected === station.id} aria-controls="execution-station-detail" onClick={() => focusStation(station.id)}><span>{station.id === "vault" ? "+" : `0${index + 1}`}</span>{station.name}<IconArrowRight size={13} /></button>)}</div>
        <p id="execution-station-detail" className={styles.detail} aria-live="polite"><strong>{active.name}.</strong> {active.detail}</p>
      </div>
      <div className={styles.caption}><span>Illustrative flow · no real money moves</span><Link href="/demo">Try an Algebra request <IconArrowRight size={13} /></Link></div>
    </section>
  );
}
