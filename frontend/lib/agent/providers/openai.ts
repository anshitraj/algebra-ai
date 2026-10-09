import OpenAI from "openai";
import type { ChatCompletionMessageParam, ChatCompletionTool } from "openai/resources/chat/completions";
import { AGENT_TOOLS, type AgentTool } from "../tools";
import { questionsAsText, runRound, runTool } from "../run-tool";
import type { PendingApproval } from "../pending-approval";
import { MAX_TOOL_ROUNDS, TOO_MANY_ROUNDS_REPLY, type ProviderTurnInput, type ProviderTurnResult } from "./types";

const toOpenAI = (tools: AgentTool[]): ChatCompletionTool[] =>
  tools.map((t) => ({ type: "function", function: { name: t.name, description: t.description, parameters: t.parameters } }));

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
  const TOOLS = toOpenAI(tools);
  const client = new OpenAI({ apiKey });
  // The system prompt is rebuilt every turn (profile/guardrails may have
  // changed), so it's never stored in history — prepended fresh instead.
  const convo: ChatCompletionMessageParam[] = (history as ChatCompletionMessageParam[]).filter((m) => m.role !== "system");
  convo.push({ role: "user", content: userMessage });
  let pendingApproval: PendingApproval | undefined;

  for (let round = 0; round < MAX_TOOL_ROUNDS; round++) {
    const response = await client.chat.completions.create(
      { model, messages: [{ role: "system", content: systemPrompt }, ...convo], tools: TOOLS },
      { signal }
    );
    const message = response.choices[0].message;
    convo.push(message);

    const toolCalls = message.tool_calls ?? [];
    if (toolCalls.length === 0) {
      return { reply: message.content ?? "", history: convo, pendingApproval };
    }
    if (message.content?.trim()) emit({ type: "text", text: message.content.trim() });

    let asked = "";
    const fnCalls = toolCalls.filter((c) => c.type === "function");
    const outcomes = await runRound(
      fnCalls,
      (c) => c.function.name,
      (c) => {
        let input: Record<string, unknown> = {};
        try {
          input = JSON.parse(c.function.arguments || "{}");
        } catch {
          // malformed JSON args — executeTool's field checks report it back to the model
        }
        return runTool(c.function.name, input, identity, emit, execute);
      }
    );
    for (const [i, call] of fnCalls.entries()) {
      const { result, pendingApproval: pa, questions } = outcomes[i];
      pendingApproval = pa ?? pendingApproval;
      if (questions) asked = questionsAsText(questions);
      convo.push({ role: "tool", tool_call_id: call.id, content: JSON.stringify(result) });
    }
    if (asked) {
      convo.push({ role: "assistant", content: asked });
      return { reply: "", history: convo, pendingApproval };
    }
  }

  return { reply: TOO_MANY_ROUNDS_REPLY, history: convo, pendingApproval };
}
