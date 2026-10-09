import { GoogleGenAI } from "@google/genai";
import type { Content, FunctionDeclaration, Part } from "@google/genai";
import { AGENT_TOOLS, type AgentTool } from "../tools";
import { questionsAsText, runRound, runTool } from "../run-tool";
import type { PendingApproval } from "../pending-approval";
import { MAX_TOOL_ROUNDS, TOO_MANY_ROUNDS_REPLY, type ProviderTurnInput, type ProviderTurnResult } from "./types";

const declarations = (tools: AgentTool[]): FunctionDeclaration[] =>
  tools.map((t) => ({ name: t.name, description: t.description, parametersJsonSchema: t.parameters }));

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
  const DECLARATIONS = declarations(tools);
  const ai = new GoogleGenAI({ apiKey });
  const contents: Content[] = [...(history as Content[]), { role: "user", parts: [{ text: userMessage }] }];
  let pendingApproval: PendingApproval | undefined;

  for (let round = 0; round < MAX_TOOL_ROUNDS; round++) {
    const response = await ai.models.generateContent({
      model,
      contents,
      config: {
        systemInstruction: systemPrompt,
        tools: [{ functionDeclarations: DECLARATIONS }],
        abortSignal: signal,
      },
    });

    const modelContent = response.candidates?.[0]?.content;
    if (modelContent) contents.push(modelContent);

    const calls = response.functionCalls ?? [];
    const text = (modelContent?.parts ?? [])
      .map((p) => p.text ?? "")
      .join("")
      .trim();
    if (calls.length === 0) {
      return { reply: text || response.text || "", history: contents, pendingApproval };
    }
    if (text) emit({ type: "text", text });

    const responseParts: Part[] = [];
    let asked = "";
    const outcomes = await runRound(
      calls,
      (c) => c.name ?? "",
      (c) => runTool(c.name ?? "", (c.args ?? {}) as Record<string, unknown>, identity, emit, execute)
    );
    for (const [i, call] of calls.entries()) {
      const { result, pendingApproval: pa, questions } = outcomes[i];
      pendingApproval = pa ?? pendingApproval;
      if (questions) asked = questionsAsText(questions);
      responseParts.push({
        functionResponse: { id: call.id, name: call.name, response: result as unknown as Record<string, unknown> },
      });
    }
    contents.push({ role: "user", parts: responseParts });
    if (asked) {
      contents.push({ role: "model", parts: [{ text: asked }] });
      return { reply: "", history: contents, pendingApproval };
    }
  }

  return { reply: TOO_MANY_ROUNDS_REPLY, history: contents, pendingApproval };
}
