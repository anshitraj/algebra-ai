import Anthropic from "@anthropic-ai/sdk";
import type { MessageParam, TextBlock, Tool, ToolResultBlockParam, ToolUseBlock } from "@anthropic-ai/sdk/resources/messages";
import { AGENT_TOOLS, type AgentTool } from "../tools";
import { questionsAsText, runRound, runTool } from "../run-tool";
import type { PendingApproval } from "../pending-approval";
import { MAX_TOOL_ROUNDS, TOO_MANY_ROUNDS_REPLY, type ProviderTurnInput, type ProviderTurnResult } from "./types";

// The tool list is identical on every call, so it's marked as a prompt-cache
// breakpoint (on the last tool) — repeat turns re-read it from cache.
const toAnthropic = (tools: AgentTool[]): Tool[] =>
  tools.map((t, i) => ({
    name: t.name,
    description: t.description,
    input_schema: t.parameters,
    ...(i === tools.length - 1 ? { cache_control: { type: "ephemeral" as const } } : {}),
  }));

export async function runTurn({
  apiKey,
  model,
  systemPrompt,
  history,
  userMessage,
  identity,
  emit,
  signal,
  execute,
  tools = AGENT_TOOLS,
}: ProviderTurnInput): Promise<ProviderTurnResult> {
  const TOOLS = toAnthropic(tools);
  const client = new Anthropic({ apiKey });
  const messages: MessageParam[] = [...(history as MessageParam[]), { role: "user", content: userMessage }];
  let pendingApproval: PendingApproval | undefined;

  for (let round = 0; round < MAX_TOOL_ROUNDS; round++) {
    const response = await client.messages.create(
      {
        model,
        max_tokens: 4096,
        system: [{ type: "text", text: systemPrompt }],
        tools: TOOLS,
        messages,
      },
      { signal }
    );

    messages.push({ role: "assistant", content: response.content });
    if (response.stop_reason === "pause_turn") continue;

    const text = response.content
      .filter((b): b is TextBlock => b.type === "text")
      .map((b) => b.text)
      .join("\n")
      .trim();
    const toolUses = response.content.filter((b): b is ToolUseBlock => b.type === "tool_use");
    if (toolUses.length === 0) {
      return { reply: text, history: messages, pendingApproval };
    }
    if (text) emit({ type: "text", text });

    const results: ToolResultBlockParam[] = [];
    let asked = "";
    const outcomes = await runRound(
      toolUses,
      (u) => u.name,
      (u) => runTool(u.name, (u.input ?? {}) as Record<string, unknown>, identity, emit, execute)
    );
    for (const [i, use] of toolUses.entries()) {
      const { result, pendingApproval: pa, questions } = outcomes[i];
      pendingApproval = pa ?? pendingApproval;
      if (questions) asked = questionsAsText(questions);
      results.push({ type: "tool_result", tool_use_id: use.id, content: JSON.stringify(result), is_error: !result.ok });
    }
    messages.push({ role: "user", content: results });
    if (asked) {
      messages.push({ role: "assistant", content: asked });
      return { reply: "", history: messages, pendingApproval };
    }
  }

  return { reply: TOO_MANY_ROUNDS_REPLY, history: messages, pendingApproval };
}
