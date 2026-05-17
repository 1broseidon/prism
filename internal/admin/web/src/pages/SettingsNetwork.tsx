import { useEffect, useState } from "preact/hooks";
import { getJSON, putJSON } from "../api/client";
import { showError, withToast } from "../state/toasts";
import { canMutate } from "../state/me";
import { mcpURLFromBase } from "../util/mcp";
import { Field } from "../components/Field";
import { SettingsLayout } from "../components/SettingsLayout";
import type { NetworkSettings } from "../api/types";

export function SettingsNetwork() {
  const mutate = canMutate();
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
    });
  };

  return (
    <SettingsLayout>
      <div class="page-header">
        <div>
          <div class="page-subtitle">
            how prism advertises itself to oauth providers and clients
          </div>
        </div>
      </div>

      {loaded === null ? (
        <div class="empty-state">loading…</div>
      ) : (
        <div class="section">
          <div class="section-header">
            <span class="section-title">network</span>
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
                pins admin sign-in callbacks and backend oauth flows. when
                blank, backend oauth derives from the inbound request.
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
                <button class="save-btn" onClick={save} disabled={!dirty}>
                  save
                </button>
                {dirty && (
                  <span class="config-dirty-marker">unsaved changes</span>
                )}
              </div>
            )}
          </div>
        </div>
      )}
    </SettingsLayout>
  );
}
