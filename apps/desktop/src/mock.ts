/** Fixture backend for `pnpm dev` in a plain browser, where Tauri's invoke is absent. Never used inside the app. */
import type {
  AgentConfig,
  AuditEntry,
  ConnectSnippet,
  Decision,
  GatewayStatus,
  PendingCall,
  Rule,
  ServerView,
} from "./types";

const iso = (secondsAgo: number) => new Date(Date.now() - secondsAgo * 1000).toISOString();

const servers: ServerView[] = [
  { id: "s1", name: "filesystem", command: "npx", args: ["-y", "@modelcontextprotocol/server-filesystem", "/home/george/Projects"], env: {}, enabled: true, status: { kind: "running", tool_count: 11 } },
  { id: "s2", name: "github", command: "docker", args: ["run", "-i", "--rm", "ghcr.io/github/github-mcp-server"], env: { GITHUB_TOKEN: "…" }, enabled: true, status: { kind: "running", tool_count: 42 } },
  { id: "s3", name: "postgres", command: "uvx", args: ["mcp-server-postgres", "postgres://localhost/app"], env: {}, enabled: true, status: { kind: "failed", error: "connection refused (127.0.0.1:5432)" } },
];
const agents: AgentConfig[] = [
  { id: "a1", name: "claude-code", client_name: "claude-code", client_version: "2.1.14", status: "approved", created_at: iso(3600 * 26), decided_at: iso(3600 * 26), connected: true },
  { id: "a2", name: "cursor", client_name: "cursor", client_version: "1.7.0", status: "approved", created_at: iso(600), decided_at: iso(590), connected: false },
  { id: "a3", name: "codex-cli", client_name: "codex-cli", client_version: "0.42.0", status: "pending", created_at: iso(12), decided_at: null, connected: true },
  { id: "a4", name: "some-random-script", client_name: "some-random-script", client_version: null, status: "denied", created_at: iso(3600 * 50), decided_at: iso(3600 * 50), connected: false },
];
let pending: PendingCall[] = [
  { id: "p1", agent_id: "a1", agent_name: "Claude Code", server_id: "s1", server_name: "filesystem", tool: "write_file", arguments: { path: "/home/george/Projects/prism/README.md", content: "# Prism\n\nA local MCP gateway…" }, requested_at: iso(23) },
];
let rules: Rule[] = [
  { id: "r1", agent_id: "a1", server_id: "s1", tool: "read_file", decision: "allow", scope: "always", created_at: iso(3600 * 5) },
  { id: "r2", agent_id: "a2", server_id: "s2", tool: "create_issue", decision: "allow", scope: "session", created_at: iso(300) },
  { id: "r3", agent_id: null, server_id: "s3", tool: null, decision: "deny", scope: "always", created_at: iso(3600 * 30) },
];
let audit: AuditEntry[] = [
  { id: "e1", at: iso(40), agent_id: "a1", agent_name: "Claude Code", server_id: "s1", tool: "read_file", verdict: "allowed", source: { kind: "rule", rule_id: "r1" }, duration_ms: 12, error: null },
  { id: "e2", at: iso(95), agent_id: "a2", agent_name: "Cursor", server_id: "s2", tool: "create_issue", verdict: "allowed", source: { kind: "human" }, duration_ms: 840, error: null },
  { id: "e3", at: iso(200), agent_id: "a1", agent_name: "Claude Code", server_id: "s3", tool: "query", verdict: "denied", source: { kind: "rule", rule_id: "r3" }, duration_ms: 1, error: null },
  { id: "e4", at: iso(500), agent_id: "a2", agent_name: "Cursor", server_id: "s1", tool: "delete_file", verdict: "timeout", source: { kind: "timeout" }, duration_ms: 120000, error: null },
  { id: "e0", at: iso(10), agent_id: "a3", agent_name: "codex-cli", server_id: "", tool: "filesystem__read_file", verdict: "denied", source: { kind: "unapproved" }, duration_ms: 0, error: "Prism has not approved 'codex-cli' yet. Open the Prism panel and approve it, then retry." },
  { id: "e5", at: iso(900), agent_id: "a1", agent_name: "Claude Code", server_id: "s2", tool: "search_code", verdict: "error", source: { kind: "human" }, duration_ms: 3300, error: "backend exited with status 1" },
];

const delay = <T,>(v: T) => new Promise<T>((r) => setTimeout(() => r(v), 60));

export const mock = {
  get_status: (): Promise<GatewayStatus> =>
    delay({ listen_port: 9086, listening: true, servers_running: servers.filter((s) => s.status.kind === "running").length, servers_total: servers.length, agent_count: agents.length, pending_count: pending.length, pending_agents: agents.filter((a) => a.status === "pending").length, auto_open_on_pending: true }),
  list_servers: () => delay(servers),
  add_server: (a: { args: { name: string; command: string; args: string[]; env: Record<string, string> } }) => {
    const s: ServerView = { id: `s${Date.now()}`, ...a.args, enabled: true, status: { kind: "starting" } };
    servers.push(s);
    return delay(s);
  },
  remove_server: (a: { serverId: string }) => { servers.splice(servers.findIndex((s) => s.id === a.serverId), 1); return delay(undefined); },
  restart_server: () => delay(undefined),
  list_agents: () => delay(agents),
  decide_agent: (a: { agentId: string; approve: boolean }) => { const ag = agents.find((x) => x.id === a.agentId); if (ag) { ag.status = a.approve ? "approved" : "denied"; ag.decided_at = iso(0); } return delay(undefined); },
  remove_agent: (a: { agentId: string }) => { agents.splice(agents.findIndex((x) => x.id === a.agentId), 1); return delay(undefined); },
  list_pending: () => delay(pending),
  decide: (a: { id: string; decision: Decision }) => {
    const call = pending.find((p) => p.id === a.id);
    pending = pending.filter((p) => p.id !== a.id);
    if (call) audit = [{ id: `e${Date.now()}`, at: iso(0), agent_id: call.agent_id, agent_name: call.agent_name, server_id: call.server_id, tool: call.tool, verdict: a.decision.verdict === "allow" ? "allowed" : "denied", source: { kind: "human" }, duration_ms: 400, error: null }, ...audit];
    return delay(undefined);
  },
  list_rules: () => delay(rules),
  delete_rule: (a: { ruleId: string }) => { rules = rules.filter((r) => r.id !== a.ruleId); return delay(undefined); },
  list_audit: (a: { limit: number }) => delay(audit.slice(0, a.limit)),
  hide_panel: () => delay(undefined),
  get_connect_snippet: (): Promise<ConnectSnippet> =>
    delay({
      url: "http://127.0.0.1:9086/mcp",
      mcp_json: JSON.stringify({ mcpServers: { prism: { url: "http://127.0.0.1:9086/mcp" } } }, null, 2),
    }),
};
