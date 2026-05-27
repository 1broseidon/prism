import { useEffect, useMemo, useState } from "preact/hooks";
import { AdvancedGuard } from "../components/policy/AdvancedGuard";
import { AdvancedSubnav } from "../components/policy/AdvancedSubnav";
import { canMutate } from "../state/me";
import { withToast } from "../state/toasts";
import {
  createGrantTemplate,
  deleteGrantTemplate,
  listGrantTemplates,
  updateGrantTemplate,
  type GrantTemplate,
} from "../api/grants";

const SAMPLE_TEMPLATE: GrantTemplate = {
  id: "tmpl-fs-write-eng-ephemeral",
  created_by: "operator",
  spec: {
    type: "prism.mcp.call",
    tool: "fs.write_file",
    backend: "local",
    args: { path: { prefix: "/workspace/${agent.prism_id}/" } },
    workspace: {
      type: { equals: "ephemeral" },
      write_mode: { equals: "stage" },
    },
    hours: "weekdays 09:00-18:00 America/Chicago",
    auth_freshness_max: 600,
    cnf_required: true,
    acr_required: "urn:prism:mfa",
  },
};

export function GrantTemplates() {
  return (
    <AdvancedGuard>
      <GrantTemplatesInner />
    </AdvancedGuard>
  );
}

function GrantTemplatesInner() {
  const [templates, setTemplates] = useState<GrantTemplate[]>([]);
  const [selected, setSelected] = useState<GrantTemplate | undefined>();
  const [text, setText] = useState(pretty(SAMPLE_TEMPLATE));
  const [error, setError] = useState("");
  const mutate = canMutate();

  const load = () => listGrantTemplates().then(setTemplates);
  useEffect(() => { load(); }, []);

  const grouped = useMemo(() => {
    const byID = new Map<string, GrantTemplate[]>();
    templates.forEach((t) => {
      const list = byID.get(t.id) || [];
      list.push(t);
      byID.set(t.id, list);
    });
    return Array.from(byID.values()).map((versions) =>
      versions.sort((a, b) => (b.version || 0) - (a.version || 0)),
    );
  }, [templates]);

  const save = async () => {
    setError("");
    let body: GrantTemplate;
    try {
      body = JSON.parse(text) as GrantTemplate;
    } catch (err) {
      setError(String(err));
      return;
    }
    const ok = await withToast(async () => {
      const saved = selected ? await updateGrantTemplate(body.id || selected.id, body) : await createGrantTemplate(body);
      setSelected(saved);
      setText(pretty(saved));
      await load();
    });
    if (ok === undefined) setError("");
  };

  const remove = async (t: GrantTemplate) => {
    if (!t.version) return;
    await withToast(async () => {
      await deleteGrantTemplate(t.id, t.version || 0);
      if (selected?.id === t.id && selected?.version === t.version) {
        setSelected(undefined);
        setText(pretty(SAMPLE_TEMPLATE));
      }
      await load();
    });
  };

  return (
    <div>
      <div class="page-header">
        <div>
          <div class="page-title">Grant templates</div>
          <div class="page-subtitle">{templates.length} version{templates.length === 1 ? "" : "s"} stored</div>
        </div>
      </div>
      <AdvancedSubnav />

      <div class="section">
        <div class="section-header">
          <span class="section-title">authoring</span>
          {mutate && <button class="section-btn" onClick={() => { setSelected(undefined); setText(pretty(SAMPLE_TEMPLATE)); }}>new template</button>}
        </div>
        <div class="card config-form" style="grid-template-columns:1fr">
          <textarea
            value={text}
            rows={18}
            spellcheck={false}
            disabled={!mutate}
            onInput={(e) => setText((e.currentTarget as HTMLTextAreaElement).value)}
            style="font-family:var(--font-mono);font-size:12px;min-height:320px"
          />
          {error && <div class="empty-state" style="padding:8px;color:var(--danger)">{error}</div>}
          {mutate && <button class="save-btn" onClick={save}>{selected ? "save new version" : "create template"}</button>}
        </div>
      </div>

      <div class="section">
        <div class="section-header">
          <span class="section-title">versions</span>
          <button class="section-btn" onClick={load}>refresh</button>
        </div>
        {grouped.length === 0 ? (
          <div class="empty-state">no grant templates</div>
        ) : (
          <table>
            <thead>
              <tr>
                <th>id</th>
                <th>version</th>
                <th>tool</th>
                <th>backend</th>
                <th>hash</th>
                <th></th>
              </tr>
            </thead>
            <tbody>
              {grouped.flatMap((versions) =>
                versions.map((t) => (
                  <tr key={`${t.id}-${t.version}`}>
                    <td><code>{t.id}</code></td>
                    <td>{t.version}</td>
                    <td>{t.spec.tool}</td>
                    <td>{t.spec.backend}</td>
                    <td><code>{t.hash}</code></td>
                    <td>
                      <button class="section-btn" onClick={() => { setSelected(t); setText(pretty(t)); }}>edit</button>
                      {mutate && t.version && <button class="section-btn" onClick={() => remove(t)}>delete</button>}
                    </td>
                  </tr>
                )),
              )}
            </tbody>
          </table>
        )}
      </div>
    </div>
  );
}

function pretty(value: unknown) {
  return JSON.stringify(value, null, 2);
}
