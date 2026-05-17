import { useEffect, useState } from "preact/hooks";
import {
  deleteJSON,
  getJSON,
  postJSON,
  putJSON,
} from "../api/client";
import { showError, withToast } from "../state/toasts";
import { canMutate, refreshMe } from "../state/me";
import { mcpURLFromBase } from "../util/mcp";
import { fmtAge } from "../util/time";
import type {
  AdminAuthPutPayload,
  AdminAuthRule,
  AdminAuthTestResponse,
  AdminAuthView,
  NetworkSettings,
  Workspace,
  WorkspaceBridgeConfig,
} from "../api/types";

const SECRET_PLACEHOLDER = "•••••••• (kept)";

interface FormState {
  issuer: string;
  clientID: string;
  clientSecret: string; // raw, only sent if non-empty
  redirectURL: string;
  scopes: string;
  groupsClaim: string;
  sessionTTL: string;
  cookieSecure: boolean;
  rules: AdminAuthRule[];
}

function emptyForm(): FormState {
  return {
    issuer: "",
    clientID: "",
    clientSecret: "",
    redirectURL: window.location.origin + "/auth/callback",
    scopes: "openid profile email",
    groupsClaim: "groups",
    sessionTTL: "24h",
    cookieSecure: false,
    rules: [],
  };
}

function fromView(v: AdminAuthView): FormState {
  if (!v.config) return emptyForm();
  return {
    issuer: v.config.issuer,
    clientID: v.config.client_id,
    clientSecret: "",
    redirectURL: v.config.redirect_url,
    scopes: (v.config.scopes || []).join(" "),
    groupsClaim: v.config.groups_claim || "groups",
    sessionTTL: v.config.session_ttl || "24h",
    cookieSecure: !!v.config.cookie_secure,
    rules: v.config.rules || [],
  };
}

function toPayload(f: FormState, secretIsSet: boolean): AdminAuthPutPayload {
  return {
    issuer: f.issuer.trim(),
    client_id: f.clientID.trim(),
    // null → server keeps existing; empty string would clear it.
    client_secret: f.clientSecret === "" && secretIsSet ? null : f.clientSecret,
    redirect_url: f.redirectURL.trim(),
    scopes: f.scopes.split(/\s+/).filter(Boolean),
    groups_claim: f.groupsClaim.trim() || "groups",
    session_ttl: f.sessionTTL.trim() || "24h",
    cookie_secure: f.cookieSecure,
    rules: f.rules,
  };
}

export function Config() {
  const [view, setView] = useState<AdminAuthView | null>(null);
  const [form, setForm] = useState<FormState>(emptyForm());
  const [dirty, setDirty] = useState(false);
  const [testing, setTesting] = useState(false);
  const [testResult, setTestResult] = useState<AdminAuthTestResponse | null>(
    null,
  );

  const load = async () => {
    try {
      const v = await getJSON<AdminAuthView>("/config/admin-auth");
      setView(v);
      setForm(fromView(v));
      setDirty(false);
    } catch (e) {
      showError(e instanceof Error ? e.message : String(e));
    }
  };

  useEffect(() => {
    load();
  }, []);

  const mutate = canMutate();

  const update = <K extends keyof FormState>(key: K, val: FormState[K]) => {
    setForm({ ...form, [key]: val });
    setDirty(true);
    setTestResult(null);
  };

  const setRules = (rules: AdminAuthRule[]) => {
    setForm({ ...form, rules });
    setDirty(true);
  };

  const onTest = async () => {
    setTesting(true);
    setTestResult(null);
    try {
      const result = await postJSON<AdminAuthTestResponse>(
        "/config/admin-auth/test",
        toPayload(form, view?.client_secret_set || false),
      );
      setTestResult(result);
    } catch (e) {
      setTestResult({
        ok: false,
        error: e instanceof Error ? e.message : String(e),
      });
    } finally {
      setTesting(false);
    }
  };

  const onSave = async () => {
    const ok = await withToast(async () => {
      const next = await putJSON<AdminAuthView>(
        "/config/admin-auth",
        toPayload(form, view?.client_secret_set || false),
      );
      setView(next);
      setForm(fromView(next));
      setDirty(false);
    });
    if (ok === undefined) return; // toast already showed error
  };

  const onEnable = async () => {
    if (
      !confirm(
        "Enable admin auth? You will be signed out and must sign in via the configured provider to continue.",
      )
    )
      return;
    await withToast(async () => {
      const next = await postJSON<AdminAuthView>(
        "/config/admin-auth/enable",
        {},
      );
      setView(next);
      // refresh /auth/me — the SPA may need to show the login screen now.
      await refreshMe();
    });
  };

  const onDisable = async () => {
    if (
      !confirm(
        "Disable admin auth? The console returns to open mode immediately.",
      )
    )
      return;
    await withToast(async () => {
      const next = await deleteJSON<AdminAuthView>(
        "/config/admin-auth/enable",
      );
      setView(next);
      await refreshMe();
    });
  };

  if (view === null) return <div class="empty-state">loading…</div>;

  const enabled = view.enabled && view.active;
  const enabledButBroken = view.enabled && !view.active;
  const hasSavedConfig = view.config !== null;

  return (
    <div>
      <div class="page-header">
        <div>
          <div class="page-title">configuration</div>
          <div class="page-subtitle">
            network, admin authentication, and console settings
          </div>
        </div>
      </div>

      <NetworkSection mutate={mutate} onSaved={load} />
      <WorkspaceBridgeSection mutate={mutate} />

      <div class="section">
        <div class="section-header">
          <span class="section-title">admin authentication</span>
          <span class="section-sub">
            oidc sign-in for the console. when disabled, the console runs open.
          </span>
        </div>

        <div class="card config-status">
          <div class="config-status-row">
            <span
              class={enabled ? "pill pill-ok" : "pill pill-neutral"}
            >
              {enabled ? "enabled" : "disabled"}
            </span>
            <div class="config-status-text">
              {enabled && view.active_issuer && (
                <span>signed-in via {view.active_issuer}</span>
              )}
              {!view.enabled && !hasSavedConfig && (
                <span>no config saved — fill the form below and test, then enable.</span>
              )}
              {!view.enabled && hasSavedConfig && (
                <span>config saved but not enabled.</span>
              )}
              {enabledButBroken && (
                <span class="config-status-warn">
                  enabled but discovery failed — fix the issuer and save, or disable to recover open mode.
                </span>
              )}
            </div>
            {mutate && (
              <div class="config-status-actions">
                {enabled || enabledButBroken ? (
                  <button class="danger-btn" onClick={onDisable}>
                    disable
                  </button>
                ) : (
                  <button
                    class="primary-btn"
                    disabled={!hasSavedConfig}
                    onClick={onEnable}
                    title={
                      hasSavedConfig
                        ? "enable admin auth using the saved config"
                        : "save a valid config first"
                    }
                  >
                    enable
                  </button>
                )}
              </div>
            )}
          </div>
        </div>
      </div>

      <div class="section">
        <div class="section-header">
          <span class="section-title">oidc provider</span>
        </div>
        <div class="card config-form">
          <Field label="issuer">
            <input
              type="text"
              class="config-input"
              value={form.issuer}
              spellcheck={false}
              placeholder="https://accounts.google.com"
              disabled={!mutate}
              onInput={(e) =>
                update("issuer", (e.target as HTMLInputElement).value)
              }
            />
          </Field>
          <Field label="client id">
            <input
              type="text"
              class="config-input"
              value={form.clientID}
              spellcheck={false}
              disabled={!mutate}
              onInput={(e) =>
                update("clientID", (e.target as HTMLInputElement).value)
              }
            />
          </Field>
          <Field label="client secret">
            <input
              type="password"
              class="config-input"
              value={form.clientSecret}
              placeholder={
                view.client_secret_set ? SECRET_PLACEHOLDER : ""
              }
              autoComplete="new-password"
              disabled={!mutate}
              onInput={(e) =>
                update(
                  "clientSecret",
                  (e.target as HTMLInputElement).value,
                )
              }
            />
          </Field>
          <Field label="redirect url">
            <input
              type="text"
              class="config-input"
              value={form.redirectURL}
              spellcheck={false}
              disabled={!mutate}
              onInput={(e) =>
                update(
                  "redirectURL",
                  (e.target as HTMLInputElement).value,
                )
              }
            />
          </Field>
          <Field label="scopes">
            <input
              type="text"
              class="config-input"
              value={form.scopes}
              spellcheck={false}
              disabled={!mutate}
              onInput={(e) =>
                update("scopes", (e.target as HTMLInputElement).value)
              }
            />
          </Field>
          <Field label="groups claim">
            <input
              type="text"
              class="config-input"
              value={form.groupsClaim}
              spellcheck={false}
              disabled={!mutate}
              onInput={(e) =>
                update(
                  "groupsClaim",
                  (e.target as HTMLInputElement).value,
                )
              }
            />
          </Field>
          <Field label="session ttl">
            <input
              type="text"
              class="config-input"
              value={form.sessionTTL}
              spellcheck={false}
              placeholder="24h"
              disabled={!mutate}
              onInput={(e) =>
                update(
                  "sessionTTL",
                  (e.target as HTMLInputElement).value,
                )
              }
            />
          </Field>
        </div>
      </div>

      <RulesEditor
        rules={form.rules}
        onChange={setRules}
        readOnly={!mutate}
      />

      {mutate && (
        <div class="section">
          <div class="config-actions">
            <button
              class="primary-btn"
              onClick={onTest}
              disabled={testing || !form.issuer}
            >
              {testing ? "testing…" : "test connection"}
            </button>
            <button
              class="save-btn"
              onClick={onSave}
              disabled={!dirty || !form.issuer || !form.clientID}
            >
              save draft
            </button>
            {dirty && (
              <span class="config-dirty-marker">unsaved changes</span>
            )}
          </div>
          {testResult && (
            <div
              class={
                testResult.ok ? "config-test-ok" : "config-test-err"
              }
            >
              {testResult.ok ? (
                <>
                  <strong>discovery ok.</strong> authorize_url:{" "}
                  <code>{testResult.authorize_url}</code>
                </>
              ) : (
                <>
                  <strong>discovery failed.</strong> {testResult.error}
                </>
              )}
            </div>
          )}
        </div>
      )}
    </div>
  );
}

function Field({
  label,
  children,
}: {
  label: string;
  children: preact.ComponentChildren;
}) {
  return (
    <div class="config-field">
      <label class="config-label">{label}</label>
      {children}
    </div>
  );
}

function RulesEditor({
  rules,
  onChange,
  readOnly,
}: {
  rules: AdminAuthRule[];
  onChange: (rules: AdminAuthRule[]) => void;
  readOnly: boolean;
}) {
  const add = () =>
    onChange([
      ...rules,
      { role: "admin", emails: [], domains: [], groups: [] },
    ]);
  const remove = (i: number) =>
    onChange(rules.filter((_, idx) => idx !== i));
  const updateRule = (i: number, patch: Partial<AdminAuthRule>) =>
    onChange(rules.map((r, idx) => (idx === i ? { ...r, ...patch } : r)));

  return (
    <div class="section">
      <div class="section-header">
        <span class="section-title">access rules</span>
        <span class="section-sub">
          first match wins. users not matched by any rule are denied.
        </span>
        {!readOnly && (
          <button class="section-btn" onClick={add}>
            + rule
          </button>
        )}
      </div>
      {rules.length === 0 ? (
        <div class="empty-state">no rules — no one can sign in. add at least one rule.</div>
      ) : (
        <div class="rules-list">
          {rules.map((r, i) => (
            <div class="card rule-card" key={i}>
              <div class="rule-header">
                <select
                  class="config-input rule-role"
                  value={r.role}
                  disabled={readOnly}
                  onChange={(e) =>
                    updateRule(i, {
                      role: (e.target as HTMLSelectElement)
                        .value as "admin" | "viewer",
                    })
                  }
                >
                  <option value="admin">admin</option>
                  <option value="viewer">viewer</option>
                </select>
                {!readOnly && (
                  <button
                    class="rule-delete"
                    onClick={() => remove(i)}
                    title="remove rule"
                  >
                    ×
                  </button>
                )}
              </div>
              <RuleMatchers
                rule={r}
                onChange={(patch) => updateRule(i, patch)}
                readOnly={readOnly}
              />
            </div>
          ))}
        </div>
      )}
    </div>
  );
}

function RuleMatchers({
  rule,
  onChange,
  readOnly,
}: {
  rule: AdminAuthRule;
  onChange: (patch: Partial<AdminAuthRule>) => void;
  readOnly: boolean;
}) {
  const ListInput = ({
    label,
    placeholder,
    value,
    onUpdate,
  }: {
    label: string;
    placeholder: string;
    value: string[] | undefined;
    onUpdate: (v: string[]) => void;
  }) => (
    <div class="rule-matcher">
      <label class="config-label">{label}</label>
      <input
        type="text"
        class="config-input"
        value={(value || []).join(", ")}
        placeholder={placeholder}
        spellcheck={false}
        disabled={readOnly}
        onInput={(e) =>
          onUpdate(
            (e.target as HTMLInputElement).value
              .split(",")
              .map((s) => s.trim())
              .filter(Boolean),
          )
        }
      />
    </div>
  );

  return (
    <div class="rule-matchers">
      <ListInput
        label="emails"
        placeholder="alice@example.com, bob@example.com"
        value={rule.emails}
        onUpdate={(emails) => onChange({ emails })}
      />
      <ListInput
        label="domains"
        placeholder="example.com"
        value={rule.domains}
        onUpdate={(domains) => onChange({ domains })}
      />
      <ListInput
        label="groups"
        placeholder="prism-admins"
        value={rule.groups}
        onUpdate={(groups) => onChange({ groups })}
      />
    </div>
  );
}

function WorkspaceBridgeSection({ mutate }: { mutate: boolean }) {
  const [config, setConfig] = useState<WorkspaceBridgeConfig | null>(null);
  const [workspaces, setWorkspaces] = useState<Workspace[]>([]);
  const [enabled, setEnabled] = useState(false);
  const [token, setToken] = useState("");
  const [saving, setSaving] = useState(false);
  const [dirty, setDirty] = useState(false);

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

  if (config === null) return null;

  const gatewayURL = window.location.origin;
  const installCommand =
    `prism-bridge workspace install --gateway ${gatewayURL} ` +
    `--token <workspace-token> --root "$PWD" --files-only`;

  return (
    <div class="section">
      <div class="section-header">
        <span class="section-title">workspace bridge</span>
        <span class="section-sub">
          outbound local stdio tools for repo-bound servers
        </span>
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
                    <span class={ws.connected ? "pill pill-ok" : "pill pill-warn"}>
                      {ws.connected ? "connected" : "stale"}
                    </span>
                  </div>
                  <div class="workspace-meta">
                    {[ws.hostname, ws.root, fmtAge(ws.last_seen)]
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
  );
}

function NetworkSection({
  mutate,
  onSaved,
}: {
  mutate: boolean;
  onSaved: () => Promise<void>;
}) {
  const [loaded, setLoaded] = useState<NetworkSettings | null>(null);
  const [adminURL, setAdminURL] = useState("");
  const [publicURL, setPublicURL] = useState("");
  const [trustProxy, setTrustProxy] = useState(false);
  const [dirty, setDirty] = useState(false);

  const load = async () => {
    try {
      const v = await getJSON<NetworkSettings>("/config/network");
      setLoaded(v);
      setAdminURL(v.admin_public_url || "");
      setPublicURL(v.public_url || "");
      setTrustProxy(!!v.trust_proxy_headers);
      setDirty(false);
    } catch (e) {
      showError(e instanceof Error ? e.message : String(e));
    }
  };

  useEffect(() => {
    load();
  }, []);

  const save = async () => {
    await withToast(async () => {
      const next = await putJSON<NetworkSettings>("/config/network", {
        admin_public_url: adminURL.trim(),
        public_url: publicURL.trim(),
        trust_proxy_headers: trustProxy,
      });
      setLoaded(next);
      setDirty(false);
      await onSaved();
    });
  };

  if (loaded === null) return null;

  return (
    <div class="section">
      <div class="section-header">
        <span class="section-title">network</span>
        <span class="section-sub">
          how prism advertises itself to oauth providers and clients
        </span>
      </div>
      <div class="card config-form" style="grid-template-columns:1fr">
        <Field label="admin public url">
          <input
            type="text"
            class="config-input"
            value={adminURL}
            spellcheck={false}
            placeholder="https://prism.example.com or http://prism.localhost:9086"
            disabled={!mutate}
            onInput={(e) => {
              setAdminURL((e.target as HTMLInputElement).value);
              setDirty(true);
            }}
          />
          <div class="hint-text" style="margin-top:4px">
            pins admin sign-in callbacks and backend oauth flows. when blank,
            backend oauth derives from the inbound request.
          </div>
        </Field>
        <Field label="gateway public url">
          <input
            type="text"
            class="config-input"
            value={publicURL}
            spellcheck={false}
            placeholder="https://prism.example.com"
            disabled={!mutate}
            onInput={(e) => {
              setPublicURL((e.target as HTMLInputElement).value);
              setDirty(true);
            }}
          />
          <div class="hint-text" style="margin-top:4px">
            advertised in /.well-known/oauth-protected-resource for mcp
            clients. optional; defaults to the listen address.
          </div>
          {publicURL.trim() && (
            <div class="hint-text" style="margin-top:4px">
              mcp clients connect at <code>{mcpURLFromBase(publicURL)}</code>
            </div>
          )}
        </Field>
        <label class="config-toggle">
          <input
            type="checkbox"
            checked={trustProxy}
            disabled={!mutate}
            onChange={(e) => {
              setTrustProxy((e.target as HTMLInputElement).checked);
              setDirty(true);
            }}
          />
          <span class="config-toggle-label">
            trust reverse-proxy headers (X-Forwarded-Proto / Host)
          </span>
          <span class="hint-text">
            enable when prism sits behind caddy, nginx, or cloudflare.
            without it, prism uses the connecting client's Host directly.
          </span>
        </label>
        {mutate && (
          <div class="config-actions" style="grid-column:1/-1">
            <button
              class="save-btn"
              onClick={save}
              disabled={!dirty}
            >
              save
            </button>
            {dirty && (
              <span class="config-dirty-marker">unsaved changes</span>
            )}
          </div>
        )}
      </div>
    </div>
  );
}
