// How each MCP client is told about Algebra's server. Every client needs the
// same two facts, the /mcp address and a bearer key, but each has its own
// file, its own field names and its own way of reading the key. The formats
// follow each client's documentation (linked from the Connect page); none of
// them has been run against a live client by us, so treat them as
// instructions rather than certified integrations.

export type McpClient = "claude-code" | "claude-desktop" | "cursor" | "codex" | "antigravity" | "openai-agents" | "stdio";

export type McpSetup = { title: string; code: string; note: string };

/** The environment variable the snippets read the key from. */
export const KEY_ENV = "ALGEBRA_API_KEY";

const KEY_HERE = "<your API key>";
const KEEP_IT =
  "Set ALGEBRA_API_KEY to a key from Projects & API keys. It is shown once: keep it in the environment of whatever runs your agent.";

/**
 * The snippet for one client. With `key` (a token shown once, on the page that
 * issued it) the key is written into the snippet; without it the snippet reads
 * ALGEBRA_API_KEY from the environment wherever the client can do that.
 */
export function mcpSetup(client: McpClient, url: string, key?: string): McpSetup {
  const json = (v: unknown) => JSON.stringify(v, null, 2);
  switch (client) {
    case "claude-code":
      return {
        title: "Run in your terminal",
        code: `claude mcp add --transport http algebra "${url}" \\\n  --header "Authorization: Bearer ${key ?? "$" + KEY_ENV}"`,
        note: key ? "" : KEEP_IT,
      };
    case "claude-desktop":
      // The header has no space after the colon, and the key sits in `env`:
      // Claude Desktop on Windows (and Cursor) pass arguments to npx without
      // quoting them, so "Authorization: Bearer x" would arrive in pieces.
      return {
        title: "MCP configuration",
        code: json({
          mcpServers: {
            algebra: {
              command: "npx",
              args: ["-y", "mcp-remote", url, "--header", "Authorization:${AUTH_HEADER}"],
              env: { AUTH_HEADER: `Bearer ${key ?? KEY_HERE}` },
            },
          },
        }),
        note: key
          ? "Leave the header as written, with no space after the colon: the key is read from AUTH_HEADER."
          : "Put a key from Projects & API keys after “Bearer ” in AUTH_HEADER. Leave the header as written, with no space after the colon.",
      };
    case "cursor":
      return {
        title: "MCP configuration",
        code: json({
          mcpServers: {
            algebra: { url, headers: { Authorization: `Bearer ${key ?? "${env:" + KEY_ENV + "}"}` } },
          },
        }),
        note: key
          ? "Cursor connects to the address directly. The key is in this file: keep it out of version control."
          : "Cursor connects to the address directly and reads ALGEBRA_API_KEY from the environment it was started in: set it, then restart Cursor.",
      };
    case "codex":
      return {
        title: "config.toml",
        code: key
          ? `[mcp_servers.algebra]\nurl = "${url}"\nhttp_headers = { "Authorization" = "Bearer ${key}" }`
          : `[mcp_servers.algebra]\nurl = "${url}"\nbearer_token_env_var = "${KEY_ENV}"`,
        note: key
          ? "The key is in this file: keep it out of version control."
          : "Codex reads ALGEBRA_API_KEY from the environment it starts in and sends it as the bearer token. Set it, then restart Codex.",
      };
    case "antigravity":
      return {
        title: "mcp_config.json",
        code: json({
          mcpServers: {
            algebra: { serverUrl: url, headers: { Authorization: `Bearer ${key ?? KEY_HERE}` } },
          },
        }),
        note: key
          ? "Antigravity reads the key from this file: keep the file private."
          : "Antigravity reads the key from this file, not from the environment: put a key from Projects & API keys in place of the placeholder, and keep the file private.",
      };
    case "openai-agents":
      return {
        title: "Python configuration",
        code: key
          ? `from agents import Agent\nfrom agents.mcp import MCPServerStreamableHttp\n\nalgebra = MCPServerStreamableHttp(params={\n    "url": "${url}",\n    "headers": {"Authorization": "Bearer ${key}"},\n})\nagent = Agent(name="Researcher", mcp_servers=[algebra])`
          : `import os\nfrom agents import Agent\nfrom agents.mcp import MCPServerStreamableHttp\n\nalgebra = MCPServerStreamableHttp(params={\n    "url": "${url}",\n    "headers": {"Authorization": "Bearer " + os.environ["${KEY_ENV}"]},\n})\nagent = Agent(name="Researcher", mcp_servers=[algebra])`,
        note: key ? "" : KEEP_IT,
      };
    case "stdio":
      return {
        title: "Connection command",
        code: `npx -y mcp-remote "${url}" \\\n  --header "Authorization: Bearer ${key ?? "$" + KEY_ENV}"`,
        note: key ? "" : KEEP_IT,
      };
  }
}
