"use client";

import Link from "next/link";
import { useEffect, useRef, useState } from "react";
import { Logo } from "@/components/logo";
import { ProviderLogo } from "@/components/provider-logo";
import { IconArrowRight, IconBan, IconCheck, IconChevronDown, IconClock, IconGauge, IconGrid, IconList, IconPlus, IconReceipt, IconSearch, IconShield, IconStop, IconStore, IconX } from "@/components/icons";
import { FEATURED_PROVIDERS } from "@/lib/paysh";

type View = "Overview" | "Providers" | "Spend passes" | "Activity";
type Execution = { id: string; capability: string; provider: string; host: string; amount: number; state: "Delivered" | "Blocked"; time: string };
const initialExecutions: Execution[] = [
  { id: "demo_001", capability: "token.price", provider: "Birdeye", host: "birdeye.so", amount: 0.003, state: "Delivered", time: "2 min ago" },
  { id: "demo_002", capability: "web.search", provider: "Exa", host: "exa.ai", amount: 0.001, state: "Delivered", time: "4 min ago" },
  { id: "demo_003", capability: "llm.chat", provider: "Gemini", host: "google.com", amount: 0.008, state: "Delivered", time: "8 min ago" },
  { id: "demo_004", capability: "token.risk", provider: "Unverified API", host: "", amount: 0, state: "Blocked", time: "12 min ago" },
];
const nav: { label: View; icon: React.ReactNode }[] = [
  { label: "Overview", icon: <IconGrid size={16} /> },
  { label: "Providers", icon: <IconStore size={16} /> },
  { label: "Spend passes", icon: <IconShield size={16} /> },
  { label: "Activity", icon: <IconList size={16} /> },
];

export function InteractiveDashboard({ standalone = false }: { standalone?: boolean }) {
  const [view, setView] = useState<View>("Overview");
  const [period, setPeriod] = useState("7 days");
  const [paused, setPaused] = useState(false);
  const [running, setRunning] = useState(false);
  const [stage, setStage] = useState(0);
  const [calls, setCalls] = useState(1284);
  const [spent, setSpent] = useState(24.68);
  const [budget, setBudget] = useState(100);
  const [ceiling, setCeiling] = useState(0.05);
  const [query, setQuery] = useState("");
  const [strategy, setStrategy] = useState("Auto");
  const [executions, setExecutions] = useState(initialExecutions);
  const [receipt, setReceipt] = useState<Execution | null>(null);
  const [notice, setNotice] = useState("");
  const timers = useRef<ReturnType<typeof setTimeout>[]>([]);
  const receiptClose = useRef<HTMLButtonElement>(null);
  const receiptTrigger = useRef<HTMLElement | null>(null);

  useEffect(() => () => timers.current.forEach(clearTimeout), []);
  useEffect(() => {
    if (!receipt) return;
    receiptClose.current?.focus();
    const key = (event: KeyboardEvent) => {
      if (event.key === "Escape") setReceipt(null);
      if (event.key === "Tab") { event.preventDefault(); receiptClose.current?.focus(); }
    };
    document.addEventListener("keydown", key);
    return () => { document.removeEventListener("keydown", key); receiptTrigger.current?.focus(); };
  }, [receipt]);

  function runRequest() {
    if (paused || running) return;
    const quote = strategy === "Fastest" ? { provider: "Allium", host: "allium.so", amount: 0.01 } : { provider: "Birdeye", host: "birdeye.so", amount: 0.003 };
    if (spent + quote.amount > budget || ceiling < quote.amount) {
      setNotice("Request blocked: the example quote exceeds your spend limits.");
      return;
    }
    setRunning(true); setStage(0); setNotice("");
    timers.current.forEach(clearTimeout);
    timers.current = [1, 2, 3, 4].map((step) => setTimeout(() => {
      setStage(step);
      if (step === 4) {
        setRunning(false); setCalls((n) => n + 1); setSpent((n) => n + quote.amount);
        setExecutions((items) => [{ id: `demo_${Date.now()}`, capability: "token.price", ...quote, state: "Delivered" as const, time: "Just now" }, ...items].slice(0, 8));
        setNotice(`Example request delivered. ${quote.amount.toFixed(3)} simulated USDC spent. Receipt created.`);
      }
    }, step * 650));
  }

  function togglePause() {
    if (!paused && running) { timers.current.forEach(clearTimeout); setRunning(false); setStage(0); }
    setPaused((p) => !p);
    setNotice(paused ? "Example agent resumed. Spend limits remain active." : "Example agent paused. All new requests are stopped.");
  }

  const filteredProviders = FEATURED_PROVIDERS.filter((p) => `${p.name} ${p.category}`.toLowerCase().includes(query.toLowerCase()));
  const shownSpent = period === "24 hours" ? spent * 0.16 : period === "30 days" ? spent * 3.8 : spent;
  return (
    <div className={`interactive-dashboard ${standalone ? "dashboard-standalone" : ""}`}>
      <div className="dashboard-topline"><div className="window-dots" aria-hidden="true"><i /><i /><i /></div><span>Algebra workspace</span><span className="demo-label">INTERACTIVE DEMO · SIMULATED DATA</span></div>
      <div className="dashboard-layout">
        <aside className="dashboard-sidebar">
          <Link href="/" className="dashboard-brand"><Logo size={26} /><span>algebra</span></Link>
          <div className="workspace-label"><span className="workspace-avatar">A</span><span>Acme workspace<small>Personal workspace</small></span></div>
          <span className="sidebar-caption">WORKSPACE</span>
          <nav aria-label="Demo dashboard">{nav.map((item) => <button key={item.label} onClick={() => { setView(item.label); setQuery(""); }} aria-current={view === item.label ? "page" : undefined} className={view === item.label ? "active" : ""}>{item.icon}<span>{item.label}</span>{item.label === "Activity" && <small>{executions.length}</small>}</button>)}</nav>
          <div className="sidebar-bottom"><div className="connection-label"><i /> Solana · USDC</div><Link href="/signup">Create your workspace <IconArrowRight size={14} /></Link><span className="sidebar-account"><span className="workspace-avatar">JD</span><span>Jamie Demo<small>Example account</small></span></span></div>
        </aside>
        <div className="dashboard-content">
          <div className="dashboard-breadcrumb"><span>Workspace <span>/</span> <strong>{view}</strong></span><span className="sandbox-pill"><i /> Sandbox</span></div>
          <div className="dashboard-heading"><div><span className="dashboard-eyebrow">YOUR AGENT OPERATIONS, AT A GLANCE</span><h3>{view === "Overview" ? "Everything, under control." : view === "Providers" ? "Find the right capability." : view === "Spend passes" ? "Your agent. Your rules." : "Every request, accounted for."}</h3><p>{view === "Overview" ? "A little autonomy. A lot of visibility." : view === "Providers" ? "Explore a catalog snapshot. Live availability appears in your console." : view === "Spend passes" ? "Try adjusting the limits for your example research agent." : "Inspect a request to see its outcome and example receipt."}</p></div><button className="dashboard-action" onClick={runRequest} disabled={paused || running}><IconPlus size={14} />{running ? "Running…" : "Run a request"}</button></div>

          {(view === "Overview" || view === "Activity") && <>
            <div className="dashboard-metrics"><Metric label="TOTAL SPEND" value={`$${shownSpent.toFixed(2)}`} detail={`${period} · USDC`} icon={<IconGauge size={14} />} /><Metric label="SUCCESSFUL CALLS" value={(period === "24 hours" ? Math.round(calls / 7) : period === "30 days" ? calls * 4 : calls).toLocaleString("en-US")} detail="Verified and delivered" icon={<IconCheck size={14} />} /><Metric label="ACTIVE SPEND PASS" value={paused ? "Paused" : "1 active"} detail={`${budget.toFixed(0)} USDC budget`} icon={<IconShield size={14} />} /></div>
            {view === "Overview" && <div className="dashboard-middle"><div className="spend-chart-card"><div className="card-title"><span>Spend overview <small>USDC</small></span><label className="period-picker"><select aria-label="Chart period" value={period} onChange={(e) => setPeriod(e.target.value)}><option>24 hours</option><option>7 days</option><option>30 days</option></select><IconChevronDown size={12} /></label></div><SpendChart period={period} /><div className="chart-legend"><span><i /> Agent spend</span><span>Within your budget <IconCheck size={12} /></span></div></div><div className="agent-status-card"><div className="card-title"><span>Research agent</span><span className={`state-pill ${paused ? "state-paused" : ""}`}><i />{paused ? "Paused" : "Active"}</span></div><div className="agent-budget"><span>${spent.toFixed(2)}<small> / ${budget.toFixed(0)}</small></span><small>Budget used</small></div><div className="budget-track"><span style={{ width: `${Math.min(spent / budget * 100, 100)}%` }} /></div><div className="agent-policy-line"><IconShield size={13} /><span>Max per call</span><strong>${ceiling.toFixed(3)}</strong></div><div className="agent-policy-line"><IconClock size={13} /><span>Routing strategy</span><select aria-label="Routing strategy" disabled={running} value={strategy} onChange={(e) => setStrategy(e.target.value)}><option>Auto</option><option>Cheapest</option><option>Fastest</option></select></div><button className={`pause-agent ${paused ? "is-paused" : ""}`} onClick={togglePause}>{paused ? <IconArrowRight size={13} /> : <IconStop size={13} />}{paused ? "Resume agent" : "Pause agent"}</button></div></div>}
            <div className="execution-card"><div className="card-title"><span>Recent activity <small>{executions.length} requests</small></span>{view === "Overview" && <button onClick={() => setView("Activity")}>View all <IconArrowRight size={12} /></button>}</div><div className="execution-table-wrap"><table className="execution-table"><thead><tr><th>CAPABILITY / PROVIDER</th><th>AMOUNT</th><th>STATUS</th><th>WHEN</th><th><span className="sr-only">Details</span></th></tr></thead><tbody>{executions.slice(0, view === "Overview" ? 4 : 8).map((item) => <tr key={item.id}><td><div className="execution-provider">{item.host ? <ProviderLogo name={item.provider} host={item.host} size={27} /> : <span className="blocked-provider"><IconBan size={15} /></span>}<span><strong>{item.capability}</strong><small>{item.provider}</small></span></div></td><td>{item.amount > 0 ? `$${item.amount.toFixed(3)}` : "—"}</td><td><span className={`state-pill ${item.state === "Blocked" ? "state-paused" : ""}`}>{item.state === "Blocked" ? <IconBan size={10} /> : <IconCheck size={10} />}{item.state}</span></td><td>{item.time}</td><td><button className="receipt-button" aria-label={`Inspect ${item.capability} request`} onClick={(e) => { receiptTrigger.current = e.currentTarget; setReceipt(item); }}><IconArrowRight size={14} /></button></td></tr>)}</tbody></table></div></div>
          </>}

          {view === "Providers" && <><label className="dashboard-search"><IconSearch size={17} /><input aria-label="Search demo providers" placeholder="Search providers or capabilities…" value={query} onChange={(e) => setQuery(e.target.value)} /><span>{filteredProviders.length} providers</span></label><div className="demo-provider-grid">{filteredProviders.map((provider) => <div className="demo-provider-card" key={provider.id}><ProviderLogo name={provider.name} host={provider.host} size={36} /><span className="provider-category">{provider.category.replace(/_/g, " ")}</span><h4>{provider.name}</h4><p>{provider.description}</p><div><span>{provider.endpoint_count} endpoints</span><button onClick={() => { setView("Spend passes"); setNotice(`Configure your example agent's limits before exploring ${provider.name}.`); }}>Configure pass <IconArrowRight size={12} /></button></div></div>)}</div>{filteredProviders.length === 0 && <p className="demo-empty">No providers found. Try “search” or “finance”.</p>}</>}

          {view === "Spend passes" && <div className="demo-pass-layout"><div className="demo-pass-card"><div className="card-title"><span><IconShield size={17} /> Research agent</span><span className={`state-pill ${paused ? "state-paused" : ""}`}>{paused ? "Paused" : "Active"}</span></div><p>A spend pass gives your agent a budget, without giving it a wallet key.</p><label className="demo-range"><span>Total budget <strong>${budget} USDC</strong></span><input aria-label="Example agent budget" disabled={running} type="range" min="25" max="500" step="25" value={budget} onChange={(e) => setBudget(Number(e.target.value))} /><small>$25 <span>$500</span></small></label><label className="demo-range"><span>Maximum per call <strong>${ceiling.toFixed(3)} USDC</strong></span><input aria-label="Example per-call limit" disabled={running} type="range" min="0.001" max="0.1" step="0.001" value={ceiling} onChange={(e) => setCeiling(Number(e.target.value))} /><small>$0.001 <span>$0.10</span></small></label><button className="pause-agent" onClick={togglePause}>{paused ? <IconArrowRight size={14} /> : <IconStop size={14} />}{paused ? "Resume agent" : "Pause all spending"}</button></div><div className="demo-pass-explainer"><IconShield size={35} /><h4>Permission has boundaries.</h4><p>Every example request checks these limits before it runs. Try setting the per-call limit below $0.003, then run a request.</p><ul><li><IconCheck size={14} /> Budget enforced before payment</li><li><IconCheck size={14} /> The agent never sees a wallet key</li><li><IconCheck size={14} /> Pause takes effect immediately</li></ul></div></div>}

          {running && <div className="request-progress" role="status">{["Discover", "Check policy", "Pay", "Receipt"].map((label, i) => <span className={stage >= i ? "current" : ""} key={label}>{stage > i ? <IconCheck size={12} /> : <i />}{label}</span>)}</div>}
          <p className="dashboard-notice" role="status">{notice}</p>
          <div className="dashboard-bottomline"><span><IconShield size={12} /> Your limits. Enforced on every request.</span>{standalone ? <Link href="/signup">Start with real data <IconArrowRight size={12} /></Link> : <Link href="/demo">Explore the full demo <IconArrowRight size={12} /></Link>}</div>
        </div>
      </div>
      {receipt && <div className="receipt-overlay" onClick={() => setReceipt(null)}><div className="receipt-modal" role="dialog" aria-modal="true" aria-labelledby="receipt-title" onClick={(e) => e.stopPropagation()}><button className="receipt-close" aria-label="Close request details" ref={receiptClose} onClick={() => setReceipt(null)}><IconX size={19} /></button><span className="modal-icon"><IconReceipt size={25} /></span><span className="dashboard-eyebrow">EXAMPLE REQUEST DETAILS</span><h3 id="receipt-title">{receipt.state === "Delivered" ? "Work delivered. Payment recorded." : "Stopped before payment."}</h3><dl><div><dt>Capability</dt><dd>{receipt.capability}</dd></div><div><dt>Provider</dt><dd>{receipt.provider}</dd></div><div><dt>Amount</dt><dd>{receipt.amount.toFixed(3)} simulated USDC</dd></div><div><dt>Outcome</dt><dd>{receipt.state}</dd></div><div><dt>Authority</dt><dd>Research agent spend pass</dd></div></dl><p>{receipt.state === "Delivered" ? "In the live product, Algebra signs an independently verifiable receipt. This is an illustrative demo record." : "The provider did not pass the example policy check. No payment was made."}</p></div></div>}
    </div>
  );
}

function Metric({ label, value, detail, icon }: { label: string; value: string; detail: string; icon: React.ReactNode }) {
  return <div className="dashboard-metric"><span>{label}{icon}</span><strong>{value}</strong><small>{detail}</small></div>;
}

export function SpendChart({ period = "7 days" }: { period?: string }) {
  const [hovered, setHovered] = useState<number | null>(null);
  const points = period === "24 hours" ? [61, 60, 48, 51, 40, 42, 30, 34, 23, 18, 22, 13] : period === "30 days" ? [65, 59, 63, 48, 53, 34, 39, 30, 33, 19, 21, 9] : [62, 56, 59, 44, 47, 35, 40, 26, 30, 18, 22, 12];
  const path = points.map((y, i) => `${i ? "L" : "M"}${i * 40 + 8},${y}`).join(" ");
  const labels = period === "24 hours" ? ["00:00", "06:00", "12:00", "18:00", "23:59"] : period === "30 days" ? ["Week 1", "Week 2", "Week 3", "Week 4", "Today"] : ["Mon", "Tue", "Wed", "Thu", "Fri", "Sat", "Sun"];
  return <div className="spend-chart"><div className="chart-y-labels"><span>$8</span><span>$4</span><span>$0</span></div><div className="chart-canvas"><svg viewBox="0 0 456 85" preserveAspectRatio="none" role="img" aria-label={`Illustrative agent spending over ${period}`}><path className="chart-gridline" d="M0 12H456M0 45H456M0 78H456" /><path className="chart-area" d={`${path}L448,85L8,85Z`} /><path className="chart-line" d={path} />{hovered !== null && <><path className="chart-cursor" d={`M${hovered * 40 + 8} 0V85`} /><circle className="chart-dot" cx={hovered * 40 + 8} cy={points[hovered]} r="3" /></>}</svg><div className="chart-hitareas">{points.map((y, i) => <button key={i} aria-label={`Example data point ${i + 1}: $${((78 - y) / 8).toFixed(2)}`} onMouseEnter={() => setHovered(i)} onFocus={() => setHovered(i)} onMouseLeave={() => setHovered(null)} onBlur={() => setHovered(null)}>{hovered === i && <span>${((78 - y) / 8).toFixed(2)}</span>}</button>)}</div><div className="chart-x-labels">{labels.map((label) => <span key={label}>{label}</span>)}</div></div></div>;
}
