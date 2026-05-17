import { useEffect, useState } from "preact/hooks";
import { deleteJSON, getJSON, postJSON, putJSON } from "../api/client";
import { showError, withToast } from "../state/toasts";
import { canMutate } from "../state/me";
import { fmtAge } from "../util/time";
import { Field } from "../components/Field";
import type {
  Workspace,
  WorkspaceBridgeConfig,
} from "../api/types";

const SECRET_PLACEHOLDER = "•••••••• (kept)";

function commaList(value: string): string[] {
  return value
    .split(",")
    .map((s) => s.trim())
    .filter(Boolean);
}

export function SettingsWorkspaces() {
  const mutate = canMutate();
  const [config, setConfig] = useState<WorkspaceBridgeConfig | null>(null);
  const [workspaces, setWorkspaces] = useState<Workspace[]>([]);
  const [enabled, setEnabled] = useState(false);
  const [token, setToken] = useState("");
  const [saving, setSaving] = useState(false);
  const [dirty, setDirty] = useState(false);
  const [workspaceID, setWorkspaceID] = useState("");
  const [workspaceType, setWorkspaceType] = useState<"virtual" | "ephemeral">("virtual");
  const [workspaceOwner, setWorkspaceOwner] = useState("");
  const [workspaceAgents, setWorkspaceAgents] = useState("");
  const [workspaceTemplates, setWorkspaceTemplates] = useState("");
  const [workspaceQuotaMB, setWorkspaceQuotaMB] = useState("");
  const [workspaceRetentionDays, setWorkspaceRetentionDays] = useState("");

  const load = async () => {
    try {
      const [cfg, ws] = await Promise.all([
        getJSON<WorkspaceBridgeConfig>("/config/workspace-bridge"),
        getJSON<Workspace[]>("/workspaces"),
      ]);
      setConfig(cfg);
      setEnabled(cfg.enabled);
      setWorkspaces(ws);
      setDirty(false);
    } catch (e) {
      showError(e instanceof Error ? e.message : String(e));
    }
  };

  useEffect(() => {
    load();
    const timer = window.setInterval(load, 10000);
    return () => window.clearInterval(timer);
  }, []);

  const save = async () => {
    setSaving(true);
    await withToast(async () => {
      const next = await putJSON<WorkspaceBridgeConfig>(
        "/config/workspace-bridge",
        { enabled, token: token.trim() || undefined },
      );
      setConfig(next);
      setToken("");
      setDirty(false);
      await load();
    });
    setSaving(false);
  };

  const disconnect = async (id: string) => {
    await withToast(async () => {
      await deleteJSON(`/workspaces/${encodeURIComponent(id)}`);
      await load();
    });
  };

  const createWorkspace = async () => {
    await withToast(async () => {
      const quotaMB = Number(workspaceQuotaMB);
      const retentionDays = Number(workspaceRetentionDays);
      await postJSON<Workspace>("/workspaces", {
        id: workspaceID.trim(),
        type: workspaceType,
        owner: workspaceOwner.trim() || undefined,
        allowed_agents: commaList(workspaceAgents),
        allowed_templates: commaList(workspaceTemplates),
        quota_bytes: Number.isFinite(quotaMB) && quotaMB > 0
          ? Math.round(quotaMB * 1024 * 1024)
          : undefined,
        retention_seconds: Number.isFinite(retentionDays) && retentionDays > 0
          ? Math.round(retentionDays * 24 * 60 * 60)
          : undefined,
      });
      setWorkspaceID("");
      setWorkspaceOwner("");
      setWorkspaceAgents("");
      setWorkspaceTemplates("");
      setWorkspaceQuotaMB("");
      setWorkspaceRetentionDays("");
      await load();
    });
  };

  if (config === null) {
    return (
      <div>
        <div class="page-header">
          <div>
            <div class="page-title">workspaces</div>
          </div>
        </div>
        <div class="empty-state">loading…</div>
      </div>
    );
  }

  const gatewayURL = window.location.origin;
  const installCommand =
    `prism-bridge workspace install --gateway ${gatewayURL} ` +
    `--token <workspace-token> --root "$PWD" --files-only`;

  return (
    <div>
      <div class="page-header">
        <div>
          <div class="page-title">workspaces</div>
          <div class="page-subtitle">
            outbound local stdio tools for repo-bound servers
          </div>
        </div>
      </div>

      <div class="section">
        <div class="section-header">
          <span class="section-title">workspace bridge</span>
        </div>
        <div class="card workspace-bridge-card">
          <div class="config-status-row">
            <span class={enabled ? "pill pill-ok" : "pill pill-neutral"}>
              {enabled ? "enabled" : "disabled"}
            </span>
            <div class="config-status-text">
              <span>
                {config.token_set
                  ? "token configured. rotate it below when needed."
                  : "set a token before enabling workspace bridges."}
              </span>
            </div>
          </div>

          <label class="config-toggle workspace-toggle">
            <input
              type="checkbox"
              checked={enabled}
              disabled={!mutate}
              onChange={(e) => {
                setEnabled((e.target as HTMLInputElement).checked);
                setDirty(true);
              }}
            />
            <span class="config-toggle-label">allow workspace bridge connections</span>
          </label>

          <Field label="workspace token">
            <input
              type="password"
              class="config-input"
              value={token}
              placeholder={config.token_set ? SECRET_PLACEHOLDER : "minimum 24 characters"}
              disabled={!mutate}
              autoComplete="new-password"
              onInput={(e) => {
                setToken((e.target as HTMLInputElement).value);
                setDirty(true);
              }}
            />
            <div class="hint-text" style="margin-top:4px">
              write-only shared secret used by local prism-bridge services.
            </div>
          </Field>

          <div class="workspace-install">
            <div class="workspace-install-label">install command</div>
            <code>{installCommand}</code>
          </div>

          {mutate && (
            <div class="inline-form server-workspace-form">
              <input
                type="text"
                class="config-input"
                value={workspaceID}
                placeholder="remote workspace id"
                spellcheck={false}
                onInput={(e) => setWorkspaceID((e.target as HTMLInputElement).value)}
              />
              <select
                value={workspaceType}
                onChange={(e) =>
                  setWorkspaceType(
                    (e.target as HTMLSelectElement).value as "virtual" | "ephemeral",
                  )
                }
              >
                <option value="virtual">remote persistent</option>
                <option value="ephemeral">temporary scratch</option>
              </select>
              <input
                type="text"
                class="config-input"
                value={workspaceOwner}
                placeholder="owner email"
                spellcheck={false}
                onInput={(e) => setWorkspaceOwner((e.target as HTMLInputElement).value)}
              />
              <input
                type="text"
                class="config-input"
                value={workspaceAgents}
                placeholder="allowed agents"
                spellcheck={false}
                onInput={(e) => setWorkspaceAgents((e.target as HTMLInputElement).value)}
              />
              <input
                type="text"
                class="config-input"
                value={workspaceTemplates}
                placeholder="allowed servers"
                spellcheck={false}
                onInput={(e) => setWorkspaceTemplates((e.target as HTMLInputElement).value)}
              />
              <input
                type="number"
                min="0"
                class="config-input"
                value={workspaceQuotaMB}
                placeholder="quota mb"
                onInput={(e) => setWorkspaceQuotaMB((e.target as HTMLInputElement).value)}
              />
              <input
                type="number"
                min="0"
                class="config-input"
                value={workspaceRetentionDays}
                placeholder="retention days"
                onInput={(e) => setWorkspaceRetentionDays((e.target as HTMLInputElement).value)}
              />
              <button
                class="section-btn"
                disabled={!workspaceID.trim()}
                onClick={createWorkspace}
              >
                create workspace
              </button>
            </div>
          )}

          {mutate && (
            <div class="config-actions">
              <button
                class="save-btn"
                onClick={save}
                disabled={saving || !dirty || (enabled && !config.token_set && !token.trim())}
              >
                {saving ? "saving…" : "save"}
              </button>
              {saving && (
                <span class="runtime-progress">
                  <span class="inline-spinner" />
                  applying
                </span>
              )}
              {dirty && !saving && (
                <span class="config-dirty-marker">unsaved changes</span>
              )}
            </div>
          )}

          <div class="workspace-list">
            {workspaces.length === 0 ? (
              <div class="empty-state">no workspace bridges connected.</div>
            ) : (
              workspaces.map((ws) => (
                <div class="workspace-row" key={ws.id}>
                  <div class="workspace-row-main">
                    <div class="workspace-title">
                      <span>{ws.id}</span>
                      <span class="pill pill-neutral">{ws.type || "proxied"}</span>
                      <span class={ws.connected ? "pill pill-ok" : "pill pill-warn"}>
                        {ws.connected ? "connected" : "stale"}
                      </span>
                    </div>
                    <div class="workspace-meta">
                      {[
                        ws.owner ? `owner ${ws.owner}` : "",
                        ws.allowed_agents?.length
                          ? `agents ${ws.allowed_agents.join(", ")}`
                          : "",
                        ws.allowed_templates?.length
                          ? `servers ${ws.allowed_templates.join(", ")}`
                          : "",
                        ws.quota_bytes ? `quota ${Math.round(ws.quota_bytes / 1024 / 1024)}mb` : "",
                        ws.retention_seconds
                          ? `retention ${Math.round(ws.retention_seconds / 86400)}d`
                          : "",
                        ws.hostname,
                        ws.root,
                        fmtAge(ws.last_seen),
                      ]
                        .filter(Boolean)
                        .join(" · ")}
                    </div>
                    {(ws.backends || []).map((backend) => (
                      <div class="workspace-backend" key={backend.id}>
                        <span>{backend.namespace}</span>
                        <span>{backend.tools?.length || 0} tools</span>
                      </div>
                    ))}
                  </div>
                  {mutate && (
                    <button
                      class="danger-btn"
                      onClick={() => disconnect(ws.id)}
                    >
                      disconnect
                    </button>
                  )}
                </div>
              ))
            )}
          </div>
        </div>
      </div>
    </div>
  );
}
