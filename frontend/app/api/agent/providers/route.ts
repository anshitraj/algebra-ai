import { NextResponse } from "next/server";
import { providerCatalog } from "@/lib/agent/models";

export const dynamic = "force-dynamic";

// Which LLM providers/models the agent can use — only providers whose API
// key is set on this server are marked available. Never returns a key.
export async function GET() {
  return NextResponse.json({ providers: providerCatalog() });
}
