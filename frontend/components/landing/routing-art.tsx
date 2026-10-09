"use client";

import { useState } from "react";
import { Logo } from "@/components/logo";
import { IconArrowRight, IconCheck, IconShield } from "@/components/icons";
import { ProviderLogo } from "@/components/provider-logo";

const nodes = [
  { name: "Birdeye", host: "birdeye.so", task: "Token intelligence", price: "0.003", className: "node-one" },
  { name: "Exa", host: "exa.ai", task: "Web search", price: "0.001", className: "node-two" },
  { name: "Quicknode", host: "quicknode.com", task: "Blockchain RPC", price: "0.001", className: "node-three" },
];

export function RoutingArt() {
  const [selected, setSelected] = useState(0);
  return (
    <div className="routing-art" aria-label="Interactive illustration of a request routed through Algebra">
      <div className="art-grid" aria-hidden="true" />
      <div className="orbit orbit-one" aria-hidden="true" /><div className="orbit orbit-two" aria-hidden="true" />
      <svg className="routing-lines" viewBox="0 0 540 480" fill="none" aria-hidden="true">
        <path d="M265 240V90H400M265 240H440M265 240v145H355M265 240H75" />
        <path className="routing-signal" d="M75 240H265V90H400M265 240H440M265 240v145H355" />
      </svg>
      <div className="agent-node"><span className="agent-asterisk">✳</span><span>Your agent</span></div>
      <div className="routing-core"><Logo size={65} /><span>algebra</span></div>
      {nodes.map((node, i) => (
        <button key={node.name} className={`provider-node ${node.className} ${selected === i ? "selected" : ""}`} onClick={() => setSelected(i)} aria-pressed={selected === i}>
          <ProviderLogo name={node.name} host={node.host} size={30} /><span><strong>{node.name}</strong><small>{node.task}</small></span><IconArrowRight size={14} />
        </button>
      ))}
      <div className="route-receipt" aria-live="polite"><span className="receipt-icon"><IconShield size={16} /></span><div><strong>Within your limits.</strong><span>{nodes[selected].price} USDC · example quote</span></div><IconCheck size={15} /></div>
      <span className="art-caption">ONE REQUEST. EVERY CHECK.</span>
    </div>
  );
}
