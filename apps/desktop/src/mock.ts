/** Fixture backend for `pnpm dev` in a plain browser, where Tauri's invoke is absent. Never used inside the app. */
import type {
  HttpAuth,
  ActivitySummary,
  AgentActivity,
  DayActivity,
  AgentConfig,
  AuditEntry,
  ConnectSnippet,
  Decision,
  GatewayStatus,
  NewRule,
  PendingCall,
  PendingSignIn,
  Rule,
  ServerView,
  Settings,
  ToolInfo,
} from "./types";

const iso = (secondsAgo: number) => new Date(Date.now() - secondsAgo * 1000).toISOString();

const servers: ServerView[] = [
  { id: "s1", name: "filesystem", command: "npx", args: ["-y", "@modelcontextprotocol/server-filesystem", "/home/george/Projects"], env: {}, credentials_stored: true, enabled: true, status: { kind: "running", tool_count: 11 }, url: null, auth: "none" },
  { id: "s2", name: "github", command: "docker", args: ["run", "-i", "--rm", "ghcr.io/github/github-mcp-server"], env: {}, credentials_stored: true, enabled: true, status: { kind: "running", tool_count: 42 }, url: null, auth: "none" },
  { id: "s3", name: "postgres", command: "uvx", args: ["mcp-server-postgres", "postgres://localhost/app"], env: {}, credentials_stored: true, enabled: true, status: { kind: "failed", error: "connection refused (127.0.0.1:5432)" }, url: null, auth: "none" },
  { id: "s4", name: "linear", command: "", args: [], env: {}, credentials_stored: false, enabled: true, status: { kind: "sign_in_required" }, url: "https://mcp.linear.app/mcp", auth: "oauth" },
  { id: "s5", name: "cloudflare docs", command: "", args: [], env: {}, credentials_stored: false, enabled: true, status: { kind: "running", tool_count: 2 }, url: "https://docs.mcp.cloudflare.com/mcp", auth: "none" },
];
const agents: AgentConfig[] = [
  { id: "host:claude-code", name: "Claude Code", client_name: "claude-code", client_version: null, status: "approved", created_at: iso(3600 * 30), decided_at: iso(3600 * 30), posture: "trusted", attention: "silent", client_id: null, host: "claude-code", connected: false, tokens: [] },
  { id: "host:codex", name: "Codex", client_name: "codex", client_version: null, status: "approved", created_at: iso(3600 * 20), decided_at: iso(3600 * 20), posture: "trusted", attention: "silent", client_id: null, host: "codex", connected: false, tokens: [] },
  { id: "a1", name: "claude-code", client_name: "claude-code", client_version: "2.1.14", status: "approved", created_at: iso(3600 * 26), decided_at: iso(3600 * 26), posture: "guided", attention: "badge", client_id: "c-claude", connected: true, tokens: [{ kind: "access", created_at: iso(1200), expires_at: iso(-2400) }, { kind: "refresh", created_at: iso(3600 * 26), expires_at: iso(-3600 * 24 * 29) }] },
  { id: "a2", name: "cursor", client_name: "cursor", client_version: "1.7.0", status: "approved", created_at: iso(600), decided_at: iso(590), posture: "first_use", attention: "silent", client_id: "c-cursor", connected: false, tokens: [{ kind: "refresh", created_at: iso(600), expires_at: iso(-3600 * 24 * 30) }] },
  { id: "a3", name: "codex-cli", client_name: "codex-cli", client_version: "0.42.0", status: "pending", created_at: iso(12), decided_at: null, posture: "first_use", attention: "silent", client_id: "c-codex", connected: false, tokens: [] },
  { id: "a4", name: "some-random-script", client_name: "some-random-script", client_version: null, status: "denied", created_at: iso(3600 * 50), decided_at: iso(3600 * 50), posture: "supervised", attention: "silent", client_id: null, connected: false, tokens: [] },
];
let pending: PendingCall[] = [
  { id: "p1", agent_id: "a1", agent_name: "Claude Code", server_id: "s1", server_name: "filesystem", tool: "write_file", arguments: { path: "/home/george/Projects/prism/README.md", content: "# Prism\n\nA local MCP gateway…" }, requested_at: iso(23), deadline: iso(-97), posture: "guided", reason: "policy" },
];
let rules: Rule[] = [
  { id: "r1", agent_id: "a1", server_id: "s1", tool: "read_file", decision: "allow", attention: null, scope: "always", expires_at: null, created_at: iso(3600 * 5) },
  { id: "r2", agent_id: "a2", server_id: "s2", tool: "create_issue", decision: "allow", attention: null, scope: "session", expires_at: null, created_at: iso(300) },
  { id: "r3", agent_id: null, server_id: "s3", tool: null, decision: "deny", attention: "notify", scope: "always", expires_at: null, created_at: iso(3600 * 30) },
  { id: "r4", agent_id: "a1", server_id: "s2", tool: null, decision: "allow", attention: null, scope: "always", expires_at: iso(-60 * 24), created_at: iso(360) },
  { id: "r5", agent_id: "a1", server_id: "s1", tool: "delete_*", decision: "ask", attention: null, scope: "always", expires_at: null, created_at: iso(3600 * 2) },
];
const HOOK_TOKEN = "k3Jx9v2mQd8sT1uWbC4eF6gH7iJ0lM_nO-pQrStUvWx";
const nativeStatus = {
  observe_native: true,
  hosts: [
    { host: "claude-code", hook_url: `http://127.0.0.1:9086/hooks/claude-code/${HOOK_TOKEN}`, last_event_at: iso(30), actions_7d: 390 },
    { host: "codex", hook_url: `http://127.0.0.1:9086/hooks/codex/${HOOK_TOKEN}`, last_event_at: iso(1800), actions_7d: 22 },
  ],
  setup: [
    { host: "claude-code", settings_path: "/home/george/.claude/settings.json", hook_installed: true, hook_trusted: null },
    { host: "codex", settings_path: "/home/george/.codex/hooks.json", hook_installed: true, hook_trusted: false },
  ],
  last_event_at: iso(30),
  actions_7d: 412,
  would_hold_7d: 3,
  by_reason: [
    { reason: "write_outside_cwd", count: 2 },
    { reason: "git_force", count: 1 },
  ],
  rules: [
    { id: "rm_outside_cwd", summary: "Recursive delete of a path outside the working directory" },
    { id: "git_force", summary: "git push --force, reset --hard, or clean -f" },
    { id: "pipe_to_shell", summary: "curl or wget piped into a shell or interpreter" },
    { id: "sudo", summary: "A command run through sudo or doas" },
    { id: "secret_read", summary: "Reading SSH keys, cloud credentials, or a .env file" },
    { id: "sensitive_write", summary: "Writing under ~/.ssh, ~/.aws, ~/.config/gh, or a shell rc file" },
    { id: "write_outside_cwd", summary: "Writing a file outside the working directory" },
  ],
};
const nat = (host: string, subject: string, extra: Partial<import("./types").NativeDetail> = {}) => ({ host, subject, cwd: "/home/george/Projects/prism", session: "s-1", via_prism: false, ...extra });
/** Mirrors prism_core::activity::needs_attention. */
const needsAttention = (e: AuditEntry) => (e.native ? !!e.native.would_hold : e.source.kind === "human" || e.source.kind === "timeout" || e.verdict === "denied");
const localDay = (at: string) => {
  const d = new Date(at);
  return `${d.getFullYear()}-${String(d.getMonth() + 1).padStart(2, "0")}-${String(d.getDate()).padStart(2, "0")}`;
};
/** A week that looks lived in: today's rows come from the feed above, earlier days are made up. */
function activitySummary(): ActivitySummary {
  const seen = audit.filter((e) => !e.native?.via_prism);
  const flagged = needsAttention;
  const byAgent = new Map<string, AgentActivity>();
  for (const e of seen) {
    const a = byAgent.get(e.agent_id) ?? { id: e.agent_id, name: e.agent_name, host: !!e.native, total: 0, attention: 0 };
    a.total += 1;
    if (flagged(e)) a.attention += 1;
    byAgent.set(e.agent_id, a);
  }
  const earlier = [
    [41, 2],
    [63, 0],
    [12, 1],
    [88, 4],
    [57, 1],
    [9, 0],
  ];
  const todayFlagged = seen.filter(flagged).length;
  const daily: DayActivity[] = earlier.map(([routine, attention], i) => {
    const d = new Date();
    d.setDate(d.getDate() - (6 - i));
    return { date: d.toISOString().slice(0, 10), routine, attention };
  });
  daily.push({ date: new Date().toISOString().slice(0, 10), routine: seen.length - todayFlagged, attention: todayFlagged });
  const agents = [...byAgent.values()].sort((x, y) => y.total - x.total);
  const priorTotal = earlier.reduce((n, [r, a]) => n + r + a, 0);
  const priorAttention = earlier.reduce((n, [, a]) => n + a, 0);
  if (agents[0]) {
    agents[0].total += priorTotal;
    agents[0].attention += priorAttention;
  }
  const mcp = seen.filter((e) => !e.native);
  return {
    days: 7,
    total: seen.length + priorTotal,
    attention: todayFlagged + priorAttention,
    mcp: {
      allowed: mcp.filter((e) => e.verdict === "allowed").length + 140,
      denied: mcp.filter((e) => e.verdict === "denied").length + 2,
      asked: mcp.filter((e) => e.source.kind === "human" || e.source.kind === "timeout").length + 3,
      errors: mcp.filter((e) => e.verdict === "error").length,
    },
    agents,
    daily,
  };
}

let audit: AuditEntry[] = [
  { id: "n1", at: iso(30), agent_id: "host:claude-code", agent_name: "Claude Code", server_id: "claude-code", tool: "Bash", verdict: "allowed", source: { kind: "observed" }, duration_ms: 0, error: null, attention: "silent", native: nat("claude-code", "cargo test -p prism-core") },
  { id: "n2", at: iso(55), agent_id: "host:claude-code", agent_name: "Claude Code", server_id: "claude-code", tool: "Edit", verdict: "allowed", source: { kind: "observed" }, duration_ms: 0, error: null, attention: "silent", native: nat("claude-code", "~/Projects/prism/crates/prism-core/src/native.rs") },
  { id: "n3", at: iso(140), agent_id: "host:claude-code", agent_name: "Claude Code", server_id: "claude-code", tool: "Bash", verdict: "allowed", source: { kind: "observed" }, duration_ms: 0, error: null, attention: "silent", native: nat("claude-code", "git push --force origin main", { would_hold: "git_force" }) },
  { id: "n6", at: iso(1800), agent_id: "host:codex", agent_name: "Codex", server_id: "codex", tool: "apply_patch", verdict: "allowed", source: { kind: "observed" }, duration_ms: 0, error: null, attention: "silent", native: nat("codex", "src/lib.rs, README.md") },
  { id: "n4", at: iso(300), agent_id: "host:claude-code", agent_name: "Claude Code", server_id: "claude-code", tool: "WebFetch", verdict: "allowed", source: { kind: "observed" }, duration_ms: 0, error: null, attention: "silent", native: nat("claude-code", "https://code.claude.com") },
  { id: "n5", at: iso(320), agent_id: "host:claude-code", agent_name: "Claude Code", server_id: "claude-code", tool: "mcp__prism__filesystem__read_file", verdict: "allowed", source: { kind: "observed" }, duration_ms: 0, error: null, attention: "silent", native: nat("claude-code", "mcp__prism__filesystem__read_file", { via_prism: true }) },
  { id: "e1", at: iso(40), agent_id: "a1", agent_name: "Claude Code", server_id: "s1", tool: "read_file", verdict: "allowed", source: { kind: "rule", rule_id: "r1" }, duration_ms: 12, error: null, attention: "silent" },
  { id: "e2", at: iso(95), agent_id: "a2", agent_name: "Cursor", server_id: "s2", tool: "create_issue", verdict: "allowed", source: { kind: "human" }, duration_ms: 840, error: null, attention: "silent" },
  { id: "e3", at: iso(200), agent_id: "a1", agent_name: "Claude Code", server_id: "s3", tool: "query", verdict: "denied", source: { kind: "rule", rule_id: "r3" }, duration_ms: 1, error: null, attention: "notify" },
  { id: "e4", at: iso(500), agent_id: "a2", agent_name: "Cursor", server_id: "s1", tool: "delete_file", verdict: "timeout", source: { kind: "timeout" }, duration_ms: 120000, error: null, attention: "badge" },
  { id: "e0", at: iso(10), agent_id: "a3", agent_name: "codex-cli", server_id: "", tool: "filesystem__read_file", verdict: "denied", source: { kind: "unapproved" }, duration_ms: 0, error: "Prism has not approved 'codex-cli' yet. Open the Prism panel and approve it, then retry.", attention: "silent" },
  { id: "e6", at: iso(70), agent_id: "a1", agent_name: "Claude Code", server_id: "s1", tool: "list_directory", verdict: "allowed", source: { kind: "posture", posture: "guided" }, duration_ms: 8, error: null, attention: "badge" },
  { id: "e5", at: iso(900), agent_id: "a1", agent_name: "Claude Code", server_id: "s2", tool: "search_code", verdict: "error", source: { kind: "human" }, duration_ms: 3300, error: "backend exited with status 1", attention: "silent" },
];
let signins: PendingSignIn[] = [
  { id: "si1", agent_id: "a2", agent_name: "cursor", client_name: "cursor", requested_at: iso(8), needs_consent: true },
];
let settings: Settings = { on_timeout: "deny", do_not_disturb: false, rate_limit_per_minute: null, hold_timeout_secs: 120, auto_open_on_pending: true };
const tools: Record<string, ToolInfo[]> = {
  s1: [
    { name: "read_file", description: "Read the complete contents of a file.", read_only: true, destructive: false },
    { name: "read_multiple_files", description: "Read several files at once.", read_only: true, destructive: false },
    { name: "write_file", description: "Create or overwrite a file.", read_only: false, destructive: true },
    { name: "edit_file", description: "Make line-based edits to a text file.", read_only: false, destructive: false },
    { name: "list_directory", description: "List files and directories.", read_only: true, destructive: false },
    { name: "delete_file", description: "Delete a file.", read_only: false, destructive: true },
  ],
  s2: [
    { name: "create_issue", description: "Open a GitHub issue.", read_only: false, destructive: false },
    { name: "search_code", description: "Search code across repositories.", read_only: true, destructive: false },
    { name: "create_pull_request", description: "Open a pull request.", read_only: false, destructive: false },
  ],
  s3: [],
};

const delay = <T,>(v: T) => new Promise<T>((r) => setTimeout(() => r(v), 60));

export const mock = {
  get_status: (): Promise<GatewayStatus> =>
    delay({ listen_port: 9086, listening: true, servers_running: servers.filter((s) => s.status.kind === "running").length, servers_total: servers.length, agent_count: agents.length, pending_count: pending.length, pending_agents: agents.filter((a) => a.status === "pending").length, pending_signins: signins.length, auto_open_on_pending: settings.auto_open_on_pending, do_not_disturb: settings.do_not_disturb }),
  list_servers: () => delay(servers),
  add_server: (a: { args: { name: string; command?: string; args?: string[]; env?: Record<string, string>; url?: string; auth?: HttpAuth; headers?: Record<string, string> } }) => {
    const remote = !!a.args.url;
    const auth = a.args.auth ?? "none";
    const s: ServerView = {
      id: `s${Date.now()}`,
      name: a.args.name,
      command: remote ? "" : (a.args.command ?? ""),
      args: [],
      env: {},
      credentials_stored: (a.args.args?.length ?? 0) > 0 || Object.keys(a.args.env ?? {}).length > 0 || Object.keys(a.args.headers ?? {}).length > 0,
      enabled: true,
      status: remote && auth === "oauth" ? { kind: "sign_in_required" } : { kind: "starting" },
      url: a.args.url ?? null,
      auth,
    };
    servers.push(s);
    return delay(s);
  },
  sign_in_server: (a: { serverId: string }) => {
    const s = servers.find((x) => x.id === a.serverId)!;
    window.setTimeout(() => { s.status = { kind: "running", tool_count: 9 }; }, 1500);
    return delay("https://mcp.example.com/authorize?client_id=prism");
  },
  sign_out_server: (a: { serverId: string }) => { servers.find((x) => x.id === a.serverId)!.status = { kind: "sign_in_required" }; return delay(undefined); },
  remove_server: (a: { serverId: string }) => { servers.splice(servers.findIndex((s) => s.id === a.serverId), 1); return delay(undefined); },
  restart_server: () => delay(undefined),
  list_agents: () => delay(agents),
  create_manual_agent: (a: { name: string }) => {
    const id = crypto.randomUUID();
    agents.push({ id, name: a.name.trim(), client_name: a.name.trim(), client_version: null, status: "approved", created_at: iso(0), decided_at: iso(0), posture: "first_use", attention: "silent", client_id: null, connected: false, tokens: [{ kind: "manual", created_at: iso(0), expires_at: null }] });
    return delay({ agent_id: id, token: `prism_demo_${crypto.randomUUID()}` });
  },
  replace_manual_token: (a: { agentId: string }) => {
    const agent = agents.find((x) => x.id === a.agentId);
    if (!agent || agent.client_id || agent.status !== "approved") return Promise.reject(new Error("Approve a manual client first."));
    agent.tokens = [{ kind: "manual", created_at: iso(0), expires_at: null }];
    return delay({ agent_id: agent.id, token: `prism_demo_${crypto.randomUUID()}` });
  },
  decide_agent: (a: { agentId: string; approve: boolean }) => { const ag = agents.find((x) => x.id === a.agentId); if (ag) { ag.status = a.approve ? "approved" : "denied"; ag.decided_at = iso(0); if (!a.approve) ag.tokens = []; } return delay(undefined); },
  revoke_agent_tokens: (a: { agentId: string }) => { const ag = agents.find((x) => x.id === a.agentId); if (ag) ag.tokens = []; return delay(undefined); },
  remove_agent: (a: { agentId: string }) => { agents.splice(agents.findIndex((x) => x.id === a.agentId), 1); return delay(undefined); },
  list_pending: () => delay(pending),
  list_signins: () => delay(signins),
  decide_signin: (a: { id: string }) => { signins = signins.filter((s) => s.id !== a.id); return delay(undefined); },
  decide: (a: { id: string; decision: Decision }) => {
    const call = pending.find((p) => p.id === a.id);
    pending = pending.filter((p) => p.id !== a.id);
    if (call) audit = [{ id: `e${Date.now()}`, at: iso(0), agent_id: call.agent_id, agent_name: call.agent_name, server_id: call.server_id, tool: call.tool, verdict: a.decision.verdict === "allow" ? "allowed" : "denied", source: { kind: "human" }, duration_ms: 400, error: null, attention: "silent" }, ...audit];
    if (call && a.decision.scope !== "once") {
      const target = a.decision.target ?? "tool";
      const minutes = typeof a.decision.scope === "object" ? a.decision.scope.for.minutes : null;
      rules = [...rules, { id: `r${Date.now()}`, agent_id: call.agent_id, server_id: target === "agent" ? null : call.server_id, tool: target === "tool" ? call.tool : null, decision: a.decision.verdict, attention: null, scope: a.decision.scope === "session" ? "session" : "always", expires_at: minutes ? iso(-60 * minutes) : null, created_at: iso(0) }];
    }
    return delay(undefined);
  },
  list_rules: () => delay(rules.filter((r) => !r.expires_at || Date.parse(r.expires_at) > Date.now())),
  delete_rule: (a: { ruleId: string }) => { rules = rules.filter((r) => r.id !== a.ruleId); return delay(undefined); },
  add_rule: (a: { rule: NewRule }) => {
    const n = a.rule;
    rules = rules.filter((r) => !(r.agent_id === n.agent_id && r.server_id === n.server_id && r.tool === n.tool));
    const rule: Rule = { id: `r${Date.now()}`, agent_id: n.agent_id, server_id: n.server_id, tool: n.tool, decision: n.decision, attention: n.attention ?? null, scope: n.scope ?? "always", expires_at: n.minutes ? iso(-60 * n.minutes) : null, created_at: iso(0) };
    rules = [...rules, rule];
    return delay(rule);
  },
  set_agent_policy: (a: { agentId: string; posture: AgentConfig["posture"] | null; attention: AgentConfig["attention"] | null }) => {
    const ag = agents.find((x) => x.id === a.agentId);
    if (!ag) return Promise.reject(new Error("mock: no such agent"));
    if (a.posture) ag.posture = a.posture;
    if (a.attention) ag.attention = a.attention;
    return delay(ag);
  },
  get_settings: () => delay(settings),
  set_settings: (a: { settings: Settings }) => { settings = { ...a.settings }; return delay(undefined); },
  list_server_tools: (a: { serverId: string }) => delay(tools[a.serverId] ?? []),
  list_audit: (a: { limit: number; agentId?: string | null; attention?: boolean | null; day?: string | null; reason?: string | null }) =>
    delay(
      audit
        .filter((e) => !a.agentId || e.agent_id === a.agentId)
        .filter((e) => !a.attention || needsAttention(e))
        .filter((e) => !a.day || localDay(e.at) === a.day)
        .filter((e) => !a.reason || e.native?.would_hold === a.reason)
        .slice(0, a.limit),
    ),
  get_activity: () => delay(activitySummary()),
  get_native_status: () => delay(nativeStatus),
  set_observe_native: (a: { on: boolean }) => { nativeStatus.observe_native = a.on; return delay(undefined); },
  rotate_hook_token: () => { for (const s of nativeStatus.setup) s.hook_installed = false; return delay(undefined); },
  get_host_hook_snippet: (a: { host: string }) => {
    const url = nativeStatus.hosts.find((h) => h.host === a.host)!.hook_url;
    const entry = a.host === "codex"
      ? { type: "command", command: `curl -s --connect-timeout 1 -m 3 -o /dev/null -X POST -H Content-Type:application/json --data-binary @- ${url}`, timeout: 5 }
      : { type: "http", url, timeout: 5 };
    return delay(JSON.stringify({ hooks: { PreToolUse: [{ hooks: [entry] }] } }, null, 2));
  },
  install_host_hook: (a: { host: string }) => { const s = nativeStatus.setup.find((h) => h.host === a.host)!; s.hook_installed = true; return delay({ path: s.settings_path, backup: s.settings_path + ".bak" }); },
  export_native_report: () => delay("/home/george/Downloads/prism-native-2026-09-06.jsonl"),
  hide_panel: () => delay(undefined),
  get_update_status: () =>
    delay({
      current: "0.2.0",
      available: { version: "0.3.0", current: "0.2.0", notes: "### Added\n- Built-in updater. Prism checks the latest release after launch and every six hours, and **Settings → Updates** installs it in place.\n\n### Fixed\n- Linux: the deb desktop entry had no category, so Cinnamon did not list Prism. It now sits under Development. See `prism.json` for the rest.", date: null, installable: true },
      checked_at: new Date().toISOString(),
      installable: true,
    }),
  check_update: () => delay({ version: "0.3.0", current: "0.2.0", notes: "### Added\n- Built-in updater. Prism checks the latest release after launch and every six hours, and **Settings → Updates** installs it in place.\n\n### Fixed\n- Linux: the deb desktop entry had no category, so Cinnamon did not list Prism. It now sits under Development. See `prism.json` for the rest.", date: null, installable: true }),
  install_update: () => delay(undefined),
  get_connect_snippet: (): Promise<ConnectSnippet> =>
    delay({
      url: "http://127.0.0.1:9086/mcp",
      mcp_json: JSON.stringify({ mcpServers: { prism: { url: "http://127.0.0.1:9086/mcp" } } }, null, 2),
    }),
};
