import { useEffect, useMemo, useState } from "preact/hooks";
import { AdvancedGuard } from "../components/policy/AdvancedGuard";
import { AdvancedSubnav } from "../components/policy/AdvancedSubnav";
import { canMutate } from "../state/me";
import { withToast } from "../state/toasts";
import {
  createGrantBinding,
  deleteGrantBinding,
  listGrantBindings,
  listGrantTemplates,
  updateGrantBinding,
  type GrantBinding,
  type GrantTemplate,
} from "../api/grants";

const SAMPLE_BINDING: GrantBinding = {
  id: "bind-engineering-senior",
  template_id: "tmpl-fs-write-eng-ephemeral",
  subjects: {
    groups: ["engineering"],
    role_required: "senior",
  },
  created_by: "operator",
};

export function GrantBindings() {
  return (
    <AdvancedGuard>
      <GrantBindingsInner />
    </AdvancedGuard>
  );
}

function GrantBindingsInner() {
  const [bindings, setBindings] = useState<GrantBinding[]>([]);
  const [templates, setTemplates] = useState<GrantTemplate[]>([]);
  const [selected, setSelected] = useState<GrantBinding | undefined>();
  const [text, setText] = useState(pretty(SAMPLE_BINDING));
  const [templateFilter, setTemplateFilter] = useState("");
  const [subjectFilter, setSubjectFilter] = useState("");
  const [error, setError] = useState("");
  const mutate = canMutate();

  const load = () => {
    const filters = templateFilter ? { template: templateFilter } : {};
    return Promise.all([listGrantBindings(filters), listGrantTemplates()]).then(([bs, ts]) => {
      setBindings(bs);
      setTemplates(ts);
    });
  };
  useEffect(() => { load(); }, []);

  const latestByID = useMemo(() => {
    const out = new Map<string, GrantTemplate>();
    templates.forEach((t) => {
      const prev = out.get(t.id);
      if (!prev || (t.version || 0) > (prev.version || 0)) out.set(t.id, t);
    });
    return out;
  }, [templates]);

  const visible = bindings.filter((b) => {
    const q = subjectFilter.trim().toLowerCase();
    if (!q) return true;
    const joined = [
      ...(b.subjects.groups || []),
      ...(b.subjects.roles || []),
      ...(b.subjects.agent_ids || []),
      b.subjects.role_required || "",
    ].join(" ").toLowerCase();
    return joined.includes(q);
  });

  const save = async () => {
    setError("");
    let body: GrantBinding;
    try {
      body = JSON.parse(text) as GrantBinding;
    } catch (err) {
      setError(String(err));
      return;
    }
    const ok = await withToast(async () => {
      const saved = selected ? await updateGrantBinding(body.id || selected.id, body) : await createGrantBinding(body);
      setSelected(saved);
      setText(pretty(saved));
      await load();
    });
    if (ok === undefined) setError("");
  };

  const remove = async (binding: GrantBinding) => {
    await withToast(async () => {
      await deleteGrantBinding(binding.id);
      if (selected?.id === binding.id) {
        setSelected(undefined);
        setText(pretty(SAMPLE_BINDING));
      }
      await load();
    });
  };

  return (
    <div>
      <div class="page-header">
        <div>
          <div class="page-title">Grant bindings</div>
          <div class="page-subtitle">{bindings.length} subject link{bindings.length === 1 ? "" : "s"}</div>
        </div>
      </div>
      <AdvancedSubnav />

      <div class="section">
        <div class="section-header">
          <span class="section-title">authoring</span>
          {mutate && <button class="section-btn" onClick={() => { setSelected(undefined); setText(pretty(SAMPLE_BINDING)); }}>new binding</button>}
        </div>
        <div class="card config-form" style="grid-template-columns:1fr">
          <textarea
            value={text}
            rows={12}
            spellcheck={false}
            disabled={!mutate}
            onInput={(e) => setText((e.currentTarget as HTMLTextAreaElement).value)}
            style="font-family:var(--font-mono);font-size:12px;min-height:220px"
          />
          {error && <div class="empty-state" style="padding:8px;color:var(--danger)">{error}</div>}
          {mutate && <button class="save-btn" onClick={save}>{selected ? "save binding" : "create binding"}</button>}
        </div>
      </div>

      <div class="section">
        <div class="section-header">
          <span class="section-title">bindings</span>
          <button class="section-btn" onClick={load}>refresh</button>
        </div>
        <div class="audit-controls">
          <select class="audit-select" value={templateFilter} onChange={(e) => setTemplateFilter((e.currentTarget as HTMLSelectElement).value)}>
            <option value="">all templates</option>
            {Array.from(latestByID.values()).map((t) => <option key={t.id} value={t.id}>{t.id}</option>)}
          </select>
          <input class="search-input audit-search" value={subjectFilter} placeholder="subject filter" onInput={(e) => setSubjectFilter((e.currentTarget as HTMLInputElement).value)} />
          <button class="audit-export" onClick={load}>apply</button>
        </div>
        {visible.length === 0 ? (
          <div class="empty-state">no grant bindings</div>
        ) : (
          <table>
            <thead>
              <tr>
                <th>id</th>
                <th>template</th>
                <th>subjects</th>
                <th>hash</th>
                <th></th>
              </tr>
            </thead>
            <tbody>
              {visible.map((b) => (
                <tr key={b.id}>
                  <td><code>{b.id}</code></td>
                  <td>{b.template_id}</td>
                  <td>{subjectSummary(b)}</td>
                  <td><code>{b.template_hash}</code></td>
                  <td>
                    <button class="section-btn" onClick={() => { setSelected(b); setText(pretty(b)); }}>edit</button>
                    {mutate && <button class="section-btn" onClick={() => remove(b)}>delete</button>}
                  </td>
                </tr>
              ))}
            </tbody>
          </table>
        )}
      </div>
    </div>
  );
}

function subjectSummary(binding: GrantBinding) {
  const parts: string[] = [];
  if (binding.subjects.groups?.length) parts.push(`groups:${binding.subjects.groups.join(",")}`);
  if (binding.subjects.roles?.length) parts.push(`roles:${binding.subjects.roles.join(",")}`);
  if (binding.subjects.agent_ids?.length) parts.push(`agents:${binding.subjects.agent_ids.join(",")}`);
  if (binding.subjects.role_required) parts.push(`requires:${binding.subjects.role_required}`);
  return parts.join(" · ") || "none";
}

function pretty(value: unknown) {
  return JSON.stringify(value, null, 2);
}
