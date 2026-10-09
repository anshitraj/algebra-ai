"use client";

import Link from "next/link";
import { useEffect, useState } from "react";
import { Footer } from "@/components/footer";
import { Nav } from "@/components/nav";
import { Logo } from "@/components/logo";
import { IconArrowRight, IconBan, IconCheck, IconExternal, IconLock, IconPlug, IconReceipt, IconShield, IconX } from "@/components/icons";
import { ProviderLogo } from "@/components/provider-logo";
import { InteractiveDashboard } from "./interactive-dashboard";
import { Reveal } from "./reveal";
import { RoutingArt } from "./routing-art";

const docs = "https://github.com/anshitraj/algebra-ai/blob/main/docs/MCP.md";
const partners = [
  { name: "Google", host: "google.com", text: "Google" },
  { name: "Birdeye", host: "birdeye.so", text: "birdeye" },
  { name: "Allium", host: "allium.so", text: "Allium" },
  { name: "Nansen", host: "nansen.ai", text: "nansen" },
  { name: "Quicknode", host: "quicknode.com", text: "Quicknode" },
  { name: "Exa", host: "exa.ai", text: "exa" },
];
const snippets = {
  cURL: `curl https://YOUR_ALGEBRA_HOST/api/v1/execute \\\n  -H "Authorization: Bearer $SPEND_PASS" \\\n  -H "Content-Type: application/json" \\\n  -d '{
    "capability": "token.price",
    "input": { "mint": "So11111111111111111111111111111111111111112" },
    "budget_max_minor": 10000,
    "provider_policy": { "strategy": "cheapest" }
  }'`,
  JavaScript: `const result = await fetch(
  "https://YOUR_ALGEBRA_HOST/api/v1/execute", {
    method: "POST",
    headers: {
      Authorization: \`Bearer \${process.env.SPEND_PASS}\`,
      "Content-Type": "application/json"
    },
    body: JSON.stringify({
      capability: "token.price",
      input: { mint: "So11111111111111111111111111111111111111112" },
      budget_max_minor: 10000
    })
  }
).then(response => response.json());`,
  MCP: `{
  "mcpServers": {
    "algebra": {
      "type": "http",
      "url": "https://YOUR_ALGEBRA_HOST/mcp",
      "headers": {
        "Authorization": "Bearer YOUR_SPEND_PASS"
      }
    }
  }
}`,
};
type SnippetTab = keyof typeof snippets;
const questions = [
  { q: "What exactly does Algebra do?", a: "Algebra routes an agent's request to a paid API, checks the spending rules you set, pays in USDC on Solana, verifies the result, and returns a signed receipt. Your agent asks for a capability, such as web search or a token price, rather than managing providers and payments itself." },
  { q: "Does my agent get access to a wallet?", a: "Your agent receives a Spend Pass with limited authority. Algebra holds the wallet key and enforces the pass's budget, per-call ceiling, provider rules and velocity limits on the server, before a payment can move." },
  { q: "Can I try it without real money?", a: "Yes. The interactive demo uses illustrative data and simulated requests. The local economic sandbox also runs the full execution flow with simulated USDC. Real devnet payments require a configured, funded wallet." },
  { q: "What can I connect?", a: "Connect agents through REST or the MCP server. Claude, Cursor, the OpenAI Agents SDK, and any HTTP client can use Algebra with a Spend Pass. The Connect page in your console includes setup snippets." },
  { q: "How do I know a request was paid only once?", a: "Algebra coordinates every request through reservation, authorization, execution and reconciliation. Ambiguous outcomes are frozen until settlement evidence is checked. Every outcome receives a signed receipt you can verify independently." },
];

export function Landing() {
  const [codeTab, setCodeTab] = useState<SnippetTab>("cURL");
  const [copied, setCopied] = useState(false);
  const [copyError, setCopyError] = useState(false);
  const [faq, setFaq] = useState<number | null>(0);
  const [threshold, setThreshold] = useState(0.05);
  const [strategy, setStrategy] = useState("cheapest");
  const [flowStep, setFlowStep] = useState(0);
  const exampleAllowed = threshold >= 0.003;

  async function copySnippet() {
    try { await navigator.clipboard.writeText(snippets[codeTab]); setCopied(true); setCopyError(false); }
    catch { setCopyError(true); }
  }
  useEffect(() => { if (!copied) return; const id = setTimeout(() => setCopied(false), 2200); return () => clearTimeout(id); }, [copied]);

  return <div className="landing">
    <a href="#main-content" className="skip-link">Skip to content</a>
    <Nav />
    <main id="main-content">
      <section className="landing-hero landing-container">
        <div className="hero-copy"><a href="#product" className="hero-announcement"><span className="status-dot" /> A new layer for the agent economy <IconArrowRight size={13} /></a><h1>Let your agents<br />do the work.<br /><span>You set the <em>limits.</em></span></h1><p>One connection to paid APIs. Smart routing, controlled spending, and a receipt for every request.<br className="desktop-break" /> Built for agents. Accountable to you.</p><div className="hero-actions"><Link href="/signup" className="button-ink">Build with Algebra <IconArrowRight size={16} /></Link><Link href="/demo" className="button-text"><span className="play-icon">▶</span> Explore the demo</Link></div><div className="hero-note"><IconShield size={13} /><span>Your agent never holds a wallet key.</span></div></div>
        <div className="hero-art-enter"><RoutingArt /></div>
        <div className="hero-bottom"><span>THE EXECUTION LAYER FOR AUTONOMOUS AGENTS</span><a href="#product">Meet your new control room <IconArrowRight size={13} /></a></div>
      </section>

      <section className="provider-band landing-container" id="providers"><p>Your agent&apos;s next capability is already out there.</p><div className="provider-wordmarks">{partners.map((partner) => <a href={`https://${partner.host}`} target="_blank" rel="noopener noreferrer" key={partner.name} aria-label={`Visit ${partner.name}`}><ProviderLogo name={partner.name} host={partner.host} size={30} /><span>{partner.text}</span></a>)}</div><span className="provider-disclaimer">Discover providers across Pay.sh, Circle, PayAI & Coinbase&apos;s x402 Bazaar. Provider names belong to their owners.</span></section>

      <section className="product-section landing-container" id="product"><Reveal><div className="section-heading"><div><span className="eyebrow"><i /> THE CONTROL ROOM</span><h2>Autonomy looks better<br />with a little <em>oversight.</em></h2></div><div><p>See what your agents spend, where requests go, and why a payment was allowed. All in one workspace.</p><Link href="/demo" className="text-link">Take it for a spin <IconArrowRight size={15} /></Link></div></div></Reveal><Reveal delay={100}><InteractiveDashboard /></Reveal><div className="product-caption"><span><i /> Try it: run a request, change the chart, or pause the agent.</span><span>Example workspace · no real money moves</span></div></section>

      <section className="how-section landing-container" id="how-it-works"><Reveal><div className="section-heading"><div><span className="eyebrow"><i /> LESS PLUMBING. MORE BUILDING.</span><h2>From intent to outcome.<br /><em>Minus the overhead.</em></h2></div><p>Your agent says what it needs. Algebra handles the work between a request and a result.</p></div></Reveal><div className="how-layout"><div className="flow-steps">{[{ title: "Ask for an outcome.", description: "A token price. A search result. An answer. One capability and a maximum spend." }, { title: "We find the right route.", description: "Algebra compares live quotes, checks provider health, and ranks the best options." }, { title: "Your rules have the final say.", description: "The Spend Pass checks every request. Outside the limits? It never gets paid." }, { title: "Get the result. Keep the proof.", description: "Payment settles in USDC. The result returns with a signed, verifiable receipt." }].map((step, i) => <button key={step.title} className={`flow-step ${flowStep === i ? "active" : ""}`} onClick={() => setFlowStep(i)} aria-pressed={flowStep === i}><span className="flow-number">0{i + 1}</span><span><strong>{step.title}</strong><p>{step.description}</p></span><IconArrowRight size={16} /></button>)}</div><Reveal className="flow-visual"><div className="flow-visual-top"><Logo size={23} /><span>One request. A complete execution.</span></div><div className="flow-request"><span>AGENT REQUEST</span><code>token.price</code><small>Maximum spend: 0.01 USDC</small></div><div className={`flow-connection ${flowStep >= 1 ? "active" : ""}`} /><div className={`flow-route ${flowStep >= 1 ? "active" : ""}`}><ProviderLogo name="Birdeye" host="birdeye.so" size={33} /><span><strong>Birdeye</strong><small>Best eligible quote</small></span><code>0.003 USDC</code><IconCheck size={15} /></div><div className={`flow-connection ${flowStep >= 2 ? "active" : ""}`} /><div className={`flow-check ${flowStep >= 2 ? "active" : ""}`}><IconShield size={20} /><span>Spend Pass policy</span><strong>{flowStep >= 2 ? "ALLOW" : "PENDING"}</strong></div><div className={`flow-connection ${flowStep >= 3 ? "active" : ""}`} /><div className={`flow-result ${flowStep === 3 ? "active" : ""}`}><IconReceipt size={20} /><span>{flowStep === 3 ? "Result delivered. Receipt signed." : "A verifiable receipt, every time."}</span></div><span className="flow-example-note">Illustrative request · click a step to explore</span></Reveal></div></section>

      <section className="security-section" id="security"><div className="landing-container"><Reveal><div className="section-heading"><div><span className="eyebrow"><i /> BUILT-IN BOUNDARIES</span><h2>Move fast.<br /><em>Stay in control.</em></h2></div><p>Give your agents room to work. Keep the decisions that matter in your hands.</p></div></Reveal><div className="security-grid"><Reveal className="security-card security-routing"><div className="mini-routing"><div className="mini-top"><span>Routing strategy</span><div className="strategy-buttons">{["cheapest", "fastest", "auto"].map((value) => <button key={value} aria-pressed={strategy === value} onClick={() => setStrategy(value)} className={strategy === value ? "active" : ""}>{value}</button>)}</div></div>{[{ name: "Birdeye", host: "birdeye.so", price: "0.003", speed: "180ms" }, { name: "Allium", host: "allium.so", price: "0.010", speed: "95ms" }, { name: "Trap-priced API", host: "", price: "25.00", speed: "—" }].map((provider, i) => <div key={provider.name} className={`mini-provider ${i === 2 ? "rejected" : i === (strategy === "fastest" ? 1 : 0) ? "chosen" : ""}`}>{provider.host ? <ProviderLogo name={provider.name} host={provider.host} size={23} /> : <IconBan size={20} />}<span>{provider.name}</span><small>{provider.speed}</small><strong>${provider.price}</strong>{i === 2 ? <IconX size={12} /> : i === (strategy === "fastest" ? 1 : 0) ? <IconCheck size={12} /> : <span className="mini-spacer" />}</div>)}</div><div className="security-card-copy"><span>01 / SMART ROUTING</span><h3>The right API.<br />At the right price.</h3><p>Compare real quotes. Skip providers that are down or overcharge. Route by cost, speed, or both.</p></div></Reveal><Reveal className="security-card" delay={80}><div className="mini-firewall"><div className="mini-top"><span>Spend firewall</span><IconShield size={16} /></div><label><span>Per-call ceiling <strong>${threshold.toFixed(3)}</strong></span><input aria-label="Try a spend firewall limit" type="range" min="0.001" max="0.1" step="0.001" value={threshold} onChange={(e) => setThreshold(Number(e.target.value))} /></label><div className={`firewall-result ${exampleAllowed ? "allowed" : "denied"}`}>{exampleAllowed ? <IconCheck size={17} /> : <IconBan size={17} />}<span><strong>{exampleAllowed ? "Request allowed" : "Request blocked"}</strong><small>$0.003 quote {exampleAllowed ? "is within your limit" : "exceeds your limit"}</small></span></div><span className="mini-helper">Drag the slider. Your rules take effect.</span></div><div className="security-card-copy"><span>02 / SPENDING AUTHORITY</span><h3>Set the budget.<br />Keep the keys.</h3><p>Per-call limits, approval thresholds, and a kill switch. Rules enforced before any money moves.</p></div></Reveal><Reveal className="security-card" delay={160}><div className="mini-receipt"><div className="mini-top"><span>Execution receipt</span><IconReceipt size={16} /></div><div><span>Policy checked</span><IconCheck size={14} /></div><div><span>Payment settled</span><IconCheck size={14} /></div><div><span>Result delivered</span><IconCheck size={14} /></div><div><span>Receipt signed</span><IconCheck size={14} /></div><p><IconLock size={12} /> Ed25519 · independently verifiable</p></div><div className="security-card-copy"><span>03 / PROOF OF EXECUTION</span><h3>Every outcome.<br />Accounted for.</h3><p>A signed receipt for every request. Trace the route, the decision, and the settlement.</p><Link href="/verify" className="text-link">Verify a receipt <IconArrowRight size={14} /></Link></div></Reveal></div><div className="security-footnote"><IconShield size={16} /><p>Permission to spend. <span>Never access to money.</span></p><span>Server-side policy. Independent of the model.</span></div></div></section>

      <section className="integrate-section landing-container" id="integrate"><Reveal className="integrate-copy"><span className="eyebrow"><i /> MADE FOR BUILDERS</span><h2>Your stack.<br />Your agent.<br /><em>One connection.</em></h2><p>Use REST or MCP. Bring Claude, Cursor, the OpenAI Agents SDK, or your own agent. Start with a Spend Pass and a single request.</p><a href={docs} className="button-ink" target="_blank" rel="noopener noreferrer">Read the documentation <IconExternal size={15} /></a><div className="integration-badges"><span><IconPlug size={14} /> REST API</span><span><Logo size={16} /> MCP compatible</span><span><IconLock size={14} /> Open source</span></div></Reveal><Reveal className="code-panel" delay={100}><div className="code-tabs" role="tablist" aria-label="Integration examples">{(Object.keys(snippets) as SnippetTab[]).map((tab, index) => <button role="tab" key={tab} id={`code-tab-${tab}`} aria-controls="integration-code" aria-selected={codeTab === tab} tabIndex={codeTab === tab ? 0 : -1} onClick={() => { setCodeTab(tab); setCopied(false); setCopyError(false); }} onKeyDown={(e) => { const tabs = Object.keys(snippets) as SnippetTab[]; if (e.key === "ArrowRight" || e.key === "ArrowLeft") { e.preventDefault(); const next = tabs[(index + (e.key === "ArrowRight" ? 1 : 2)) % 3]; setCodeTab(next); document.getElementById(`code-tab-${next}`)?.focus(); } }} className={codeTab === tab ? "active" : ""}>{tab}</button>)}<button className="code-copy" onClick={copySnippet}>{copied ? <><IconCheck size={12} /> Copied</> : "Copy code"}</button></div><div className="code-filename"><span className="status-dot" /> {codeTab === "MCP" ? "mcp.config.json" : codeTab === "JavaScript" ? "execute.ts" : "your-first-request.sh"}<span>QUICK START</span></div><pre id="integration-code" role="tabpanel" aria-labelledby={`code-tab-${codeTab}`} tabIndex={0}><code>{snippets[codeTab]}</code></pre><div className="code-panel-footer"><span><IconCheck size={13} /> 10000 micro-USDC = 0.01 USDC</span><span role="status">{copyError ? "Select the code to copy manually." : "Replace host and pass before running."}</span></div></Reveal></section>

      <section className="faq-section landing-container"><Reveal><span className="eyebrow"><i /> A FEW GOOD QUESTIONS</span><h2>Glad you <em>asked.</em></h2><p>The details behind the autonomy.</p></Reveal><div className="faq-list">{questions.map((question, index) => <div className={`faq-item ${faq === index ? "open" : ""}`} key={question.q}><button id={`faq-question-${index}`} aria-expanded={faq === index} aria-controls={`faq-answer-${index}`} onClick={() => setFaq(faq === index ? null : index)}><span>{question.q}</span><IconPlusOrMinus open={faq === index} /></button><div id={`faq-answer-${index}`} role="region" aria-labelledby={`faq-question-${index}`} hidden={faq !== index}><p>{question.a}</p></div></div>)}</div></section>

      <section className="closing-cta landing-container"><Reveal><span className="eyebrow"><i /> THE NEXT THING YOU BUILD</span><h2>Give your agent<br />a little <em>independence.</em></h2><p>And yourself a lot more peace of mind.</p><div><Link href="/signup" className="button-ink">Get started with Algebra <IconArrowRight size={16} /></Link><Link href="/demo" className="button-text">Explore the workspace <IconArrowRight size={16} /></Link></div></Reveal><div className="closing-art" aria-hidden="true"><Logo size={160} /><span className="closing-orbit" /></div></section>
    </main>
    <Footer />
  </div>;
}

function IconPlusOrMinus({ open }: { open: boolean }) {
  return <svg width="16" height="16" viewBox="0 0 16 16" fill="none" aria-hidden="true"><path d="M3 8h10" stroke="currentColor" strokeWidth="1.5" /><path className={open ? "faq-minus" : ""} d="M8 3v10" stroke="currentColor" strokeWidth="1.5" /></svg>;
}
