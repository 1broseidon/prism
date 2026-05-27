// Advanced — cross-cutting read view (spec §11, renamed from PowerTools in
// the policy-refine rework / task-38). Three sections:
//
//   1. Scopes — every AgentPolicy.Grant scope string flattened with the
//      agent it's attached to (read-only; CRUD happens on the per-agent
//      Direct Grants section or via the legacy AgentPolicy editor).
//   2. Templates — all GrantTemplate versions with binding counts.
//   3. Bindings — all GrantBinding rows with the template + subjects.
//
// The page is itself Advanced-gated by the route wrapper in app.tsx, so
// landing here implies the toggle is ON. The page renders inside the policy
// shell (sidebar + main) so operators keep a single visual context.
//
// CRUD on templates/bindings still lives in /policy/advanced/templates and
// /policy/advanced/bindings (both gated). This page is a read aggregation
// only.

import { useEffect, useMemo, useState } from "preact/hooks";
import { useLocation } from "preact-iso";
import { SubjectSidebar } from "../components/policy/SubjectSidebar";
import {
  listGrantBindings,
  listGrantTemplates,
  type GrantBinding,
  type GrantTemplate,
} from "../api/grants";
import { agents } from "../state";
import type { Agent } from "../api/types";

const SYSTEM_SCOPE = "mcp:connect";

interface ScopeRow {
  scope: string;
  agentLabel: string;
  agentID: string;
  agentRoute: string;
  kind: "grant" | "deny";
}

export function Advanced() {
  const loc = useLocation();
  const agentList = agents.data.value || [];
  const [templates, setTemplates] = useState<GrantTemplate[]>([]);
  const [bindings, setBindings] = useState<GrantBinding[]>([]);
  const [error, setError] = useState<string>("");
  const [loading, setLoading] = useState(true);

  useEffect(() => {
    let cancelled = false;
    setLoading(true);
    setError("");
    (async () => {
      try {
        const [t, b] = await Promise.all([
          listGrantTemplates(),
          listGrantBindings(),
        ]);
        if (cancelled) return;
        setTemplates(t);
        setBindings(b);
      } catch (e) {
        if (cancelled) return;
        setError(e instanceof Error ? e.message : String(e));
      } finally {
        if (!cancelled) setLoading(false);
      }
    })();
    return () => {
      cancelled = true;
    };
  }, []);

  const scopeRows = useMemo<ScopeRow[]>(
    () => flattenScopes(agentList),
    [agentList],
  );

  const bindingsByTemplate = useMemo(() => {
    const m = new Map<string, number>();
    for (const b of bindings) {
      m.set(b.template_id, (m.get(b.template_id) || 0) + 1);
    }
    return m;
  }, [bindings]);

  const groupedTemplates = useMemo(() => {
    const byID = new Map<string, GrantTemplate[]>();
    for (const t of templates) {
      const list = byID.get(t.id) || [];
      list.push(t);
      byID.set(t.id, list);
    }
    return Array.from(byID.values()).map((versions) =>
      versions.sort((a, b) => (b.version || 0) - (a.version || 0)),
    );
  }, [templates]);

  return (
    <div class="policy-shell">
      <SubjectSidebar activePath={loc.path || "/policy/advanced"} />
      <div class="policy-main">
        <div class="page-header">
          <div>
            <div class="page-title">Advanced — cross-cutting view</div>
            <div class="page-subtitle">
              Read-only aggregation of every raw scope, template, and binding
              across the install. CRUD lives in{" "}
              <a class="link-accent" href="/policy/advanced/templates">
                Templates
              </a>{" "}
              and{" "}
              <a class="link-accent" href="/policy/advanced/bindings">
                Bindings
              </a>
              .
            </div>
          </div>
        </div>

        {error && (
          <div class="empty-state" style="color:var(--danger)">
            could not load Advanced data: {error}
          </div>
        )}

        <div class="section">
          <div class="section-header">
            <span class="section-title">
              raw scopes ({scopeRows.length})
            </span>
            <span class="section-sub">
              AgentPolicy.Grant / AgentPolicy.Deny strings flattened with
              their owning agent. Edit on the agent's policy page.
            </span>
          </div>
          {scopeRows.length === 0 ? (
            <div class="empty-state">
              no per-agent scope strings recorded.
            </div>
          ) : (
            <table>
              <thead>
                <tr>
                  <th>scope</th>
                  <th>kind</th>
                  <th>agent</th>
                </tr>
              </thead>
              <tbody>
                {scopeRows.map((r, i) => (
                  <tr key={`${r.agentID}-${r.kind}-${r.scope}-${i}`}>
                    <td>
                      <code>{r.scope}</code>
                    </td>
                    <td>{r.kind}</td>
                    <td>
                      <a
                        class="link-accent"
                        href={`/policy/agents/${encodeURIComponent(r.agentRoute)}`}
                      >
                        {r.agentLabel}
                      </a>
                    </td>
                  </tr>
                ))}
              </tbody>
            </table>
          )}
        </div>

        <div class="section">
          <div class="section-header">
            <span class="section-title">
              templates ({templates.length} version{templates.length === 1 ? "" : "s"})
            </span>
            <a class="section-btn" href="/policy/advanced/templates">
              author templates
            </a>
          </div>
          {loading ? (
            <div class="empty-state">loading…</div>
          ) : groupedTemplates.length === 0 ? (
            <div class="empty-state">no grant templates.</div>
          ) : (
            <table>
              <thead>
                <tr>
                  <th>id</th>
                  <th>latest version</th>
                  <th>tool</th>
                  <th>backend</th>
                  <th>bindings</th>
                </tr>
              </thead>
              <tbody>
                {groupedTemplates.map((versions) => {
                  const head = versions[0];
                  const count = bindingsByTemplate.get(head.id) || 0;
                  return (
                    <tr key={head.id}>
                      <td>
                        <code>{head.id}</code>
                      </td>
                      <td>{head.version ?? "—"}</td>
                      <td>{head.spec.tool}</td>
                      <td>{head.spec.backend}</td>
                      <td>{count}</td>
                    </tr>
                  );
                })}
              </tbody>
            </table>
          )}
        </div>

        <div class="section">
          <div class="section-header">
            <span class="section-title">
              bindings ({bindings.length})
            </span>
            <a class="section-btn" href="/policy/advanced/bindings">
              author bindings
            </a>
          </div>
          {loading ? (
            <div class="empty-state">loading…</div>
          ) : bindings.length === 0 ? (
            <div class="empty-state">no grant bindings.</div>
          ) : (
            <table>
              <thead>
                <tr>
                  <th>id</th>
                  <th>template</th>
                  <th>subjects</th>
                  <th>hash</th>
                </tr>
              </thead>
              <tbody>
                {bindings.map((b) => (
                  <tr key={b.id}>
                    <td>
                      <code>{b.id}</code>
                    </td>
                    <td>
                      <code>{b.template_id}</code>
                    </td>
                    <td>{subjectSummary(b)}</td>
                    <td>
                      <code>{b.template_hash || "—"}</code>
                    </td>
                  </tr>
                ))}
              </tbody>
            </table>
          )}
        </div>
      </div>
    </div>
  );
}

function flattenScopes(list: Agent[]): ScopeRow[] {
  const out: ScopeRow[] = [];
  for (const a of list) {
    const label = a.label || a.description || a.client_id;
    const route = a.prism_id || a.client_id;
    const id = a.prism_id || a.client_id;
    const grants = (a.policy?.grant || []).filter((s) => s !== SYSTEM_SCOPE);
    const denies = a.policy?.deny || [];
    for (const s of grants) {
      out.push({
        scope: s,
        kind: "grant",
        agentLabel: label,
        agentID: id,
        agentRoute: route,
      });
    }
    for (const s of denies) {
      out.push({
        scope: s,
        kind: "deny",
        agentLabel: label,
        agentID: id,
        agentRoute: route,
      });
    }
  }
  out.sort((a, b) => {
    if (a.kind !== b.kind) return a.kind === "grant" ? -1 : 1;
    if (a.scope !== b.scope) return a.scope.localeCompare(b.scope);
    return a.agentLabel.localeCompare(b.agentLabel);
  });
  return out;
}

function subjectSummary(binding: GrantBinding): string {
  const parts: string[] = [];
  if (binding.subjects.groups?.length)
    parts.push(`groups:${binding.subjects.groups.join(",")}`);
  if (binding.subjects.roles?.length)
    parts.push(`roles:${binding.subjects.roles.join(",")}`);
  if (binding.subjects.agent_ids?.length)
    parts.push(`agents:${binding.subjects.agent_ids.join(",")}`);
  if (binding.subjects.role_required)
    parts.push(`requires:${binding.subjects.role_required}`);
  return parts.join(" · ") || "none";
}
