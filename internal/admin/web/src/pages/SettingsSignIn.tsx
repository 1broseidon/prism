import { useEffect, useState } from "preact/hooks";
import {
  deleteJSON,
  getJSON,
  postJSON,
  putJSON,
} from "../api/client";
import { showError, withToast } from "../state/toasts";
import { canMutate, refreshMe } from "../state/me";
import { Field } from "../components/Field";
import { SettingsLayout } from "../components/SettingsLayout";
import type {
  AdminAuthPutPayload,
  AdminAuthRule,
  AdminAuthTestResponse,
  AdminAuthView,
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
    client_secret: f.clientSecret === "" && secretIsSet ? null : f.clientSecret,
    redirect_url: f.redirectURL.trim(),
    scopes: f.scopes.split(/\s+/).filter(Boolean),
    groups_claim: f.groupsClaim.trim() || "groups",
    session_ttl: f.sessionTTL.trim() || "24h",
    cookie_secure: f.cookieSecure,
    rules: f.rules,
  };
}

export function SettingsSignIn() {
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
    if (ok === undefined) return;
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

  if (view === null) {
    return (
      <SettingsLayout>
        <div class="empty-state">loading…</div>
      </SettingsLayout>
    );
  }

  const enabled = view.enabled && view.active;
  const enabledButBroken = view.enabled && !view.active;
  const hasSavedConfig = view.config !== null;

  return (
    <SettingsLayout>
      <div class="page-header">
        <div>
          <div class="page-subtitle">
            console sign-in. when disabled, the console runs open.
          </div>
        </div>
      </div>

      <div class="section">
        <div class="card config-status">
          <div class="config-status-row">
            <span class={enabled ? "pill pill-ok" : "pill pill-neutral"}>
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
              placeholder={view.client_secret_set ? SECRET_PLACEHOLDER : ""}
              autoComplete="new-password"
              disabled={!mutate}
              onInput={(e) =>
                update("clientSecret", (e.target as HTMLInputElement).value)
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
                update("redirectURL", (e.target as HTMLInputElement).value)
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
                update("groupsClaim", (e.target as HTMLInputElement).value)
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
                update("sessionTTL", (e.target as HTMLInputElement).value)
              }
            />
          </Field>
        </div>
      </div>

      <RulesEditor rules={form.rules} onChange={setRules} readOnly={!mutate} />

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
              class={testResult.ok ? "config-test-ok" : "config-test-err"}
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
    </SettingsLayout>
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
        <div class="empty-state">
          no rules — no one can sign in. add at least one rule.
        </div>
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
                      role: (e.target as HTMLSelectElement).value as
                        | "admin"
                        | "viewer",
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
