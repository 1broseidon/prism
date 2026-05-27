// AgentsRoles — the Roles tab on /agents. Lists every role with its member
// count and routes into the per-role detail page for membership management.
//
// Roles vs Groups (the one-liner explainer rendered up top): Roles are
// labels on identity. Today every role is locally assigned (the "Local"
// badge on each detail row reflects that). In a future iteration roles can
// be asserted by an upstream IdP via OIDC claims, at which point those will
// show an "External" badge instead. We render the badge column now so the
// surface doesn't need restructuring later.

import { useEffect, useMemo, useState } from "preact/hooks";
import { agents } from "../state";
import { listRoles, summarizeRoles, type RoleSummary } from "../api/identity";
import { canMutate } from "../state/me";
import { AgentsShell } from "./Agents";

export function AgentsRoles() {
  const agentList = agents.data.value || [];
  const [listedRoles, setListedRoles] = useState<RoleSummary[]>([]);
  const [extras, setExtras] = useState<string[]>([]);
  const [query, setQuery] = useState("");
  const [adding, setAdding] = useState(false);
  const [newName, setNewName] = useState("");

  useEffect(() => {
    let cancelled = false;
    (async () => {
      try {
        const roles = await listRoles();
        if (!cancelled) setListedRoles(roles);
      } catch {
        if (!cancelled) setListedRoles([]);
      }
    })();
    return () => {
      cancelled = true;
    };
  }, []);

  const summaries = useMemo(() => {
    const fromAgents = summarizeRoles(agentList);
    const counts = new Map(fromAgents.map((r) => [r.name, r.memberCount]));
    const bySlug = new Map<string, RoleSummary>();
    for (const r of listedRoles) {
      const slug = r.id || r.name;
      bySlug.set(slug, {
        ...r,
        memberCount:
          r.memberCount ||
          counts.get(slug) ||
          counts.get(r.name) ||
          counts.get(r.display_name || "") ||
          0,
      });
      counts.delete(slug);
      counts.delete(r.name);
      if (r.display_name) counts.delete(r.display_name);
    }
    for (const [name, memberCount] of counts) {
      bySlug.set(name, { name, memberCount });
    }
    for (const name of extras) {
      if (!bySlug.has(name)) {
        bySlug.set(name, { name, memberCount: 0 });
      }
    }
    return Array.from(bySlug.values())
      .sort((a, b) => a.name.localeCompare(b.name));
  }, [agentList, listedRoles, extras]);

  const filtered = useMemo(() => {
    const q = query.trim().toLowerCase();
    if (!q) return summaries;
    return summaries.filter((r) => r.name.toLowerCase().includes(q));
  }, [summaries, query]);

  const submit = () => {
    const name = newName.trim();
    if (!name) {
      setAdding(false);
      return;
    }
    setExtras((prev) => (prev.includes(name) ? prev : [...prev, name]));
    setAdding(false);
    setNewName("");
  };

  const subtitle = `${summaries.length} role${
    summaries.length === 1 ? "" : "s"
  } · membership lives here, permissions live in Policy`;

  return (
    <AgentsShell subtitle={subtitle}>
      <p class="tab-explainer">
        Labels on agent identity. Assigned here or asserted by an upstream
        identity provider.
      </p>

      <div class="section">
        <div class="section-header">
          <span class="section-title">roles ({summaries.length})</span>
          <div class="section-actions">
            {summaries.length > 0 && (
              <input
                type="search"
                class="search-input"
                placeholder="search roles…"
                value={query}
                onInput={(e) =>
                  setQuery((e.target as HTMLInputElement).value)
                }
              />
            )}
            {canMutate() && !adding && (
              <button
                class="section-btn"
                onClick={() => {
                  setAdding(true);
                  setNewName("");
                }}
              >
                + add role
              </button>
            )}
          </div>
        </div>

        {adding && (
          <div class="agents-add-row">
            <input
              type="text"
              class="search-input"
              placeholder="role name"
              autofocus
              value={newName}
              onInput={(e) =>
                setNewName((e.target as HTMLInputElement).value)
              }
              onKeyDown={(e) => {
                if (e.key === "Enter") submit();
                if (e.key === "Escape") {
                  setAdding(false);
                  setNewName("");
                }
              }}
            />
            <button class="section-btn" onClick={submit}>
              save
            </button>
            <button
              class="section-btn"
              onClick={() => {
                setAdding(false);
                setNewName("");
              }}
            >
              cancel
            </button>
          </div>
        )}

        {summaries.length === 0 ? (
          <div class="empty-callout">
            <div class="empty-callout-title">no roles yet</div>
            <div class="empty-callout-body">
              roles are labels on agent identity. Create one above, then open
              its detail page to assign agents. Future versions will also let
              an IdP assert roles via OIDC claims.
            </div>
          </div>
        ) : filtered.length === 0 ? (
          <div class="empty-state">no roles match “{query}”.</div>
        ) : (
          <ul class="agents-tab-list" role="list">
            {filtered.map((r) => (
              <li class="agents-tab-row" key={r.id || r.name}>
                <a
                  class="agents-tab-row-link"
                  href={`/agents/roles/${encodeURIComponent(r.id || r.name)}`}
                >
                  <div class="agents-tab-row-main">
                    <span class="agents-tab-row-name">
                      {r.display_name || r.name}
                    </span>
                    <span class="agents-tab-row-meta">
                      {r.memberCount} member
                      {r.memberCount === 1 ? "" : "s"}
                    </span>
                  </div>
                  <span class="agents-tab-row-chevron">›</span>
                </a>
              </li>
            ))}
          </ul>
        )}
      </div>
    </AgentsShell>
  );
}
