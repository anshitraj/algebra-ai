import Link from "next/link";
import type { Metadata } from "next";
import { InteractiveDashboard } from "@/components/landing/interactive-dashboard";
import { IconArrowLeft, IconArrowRight } from "@/components/icons";

export const metadata: Metadata = { title: "Explore the workspace — Algebra", description: "Try Algebra's interactive workspace with simulated requests, adjustable spend limits and example receipts." };

export default function DemoPage() {
  return <main className="demo-page"><div className="demo-page-nav"><Link href="/"><IconArrowLeft size={16} /> Back to Algebra</Link><span>Explore freely. No money moves.</span><Link href="/signup">Create an account <IconArrowRight size={16} /></Link></div><InteractiveDashboard standalone /></main>;
}
