import { useEffect, useState } from "preact/hooks";
import { useLocation, useRoute } from "preact-iso";
import { agents, groups, backends, events } from "../state";
import { deleteJSON, getJSON, putJSON } from "../api/client";
import { withToast } from "../state/toasts";
import { canMutate } from "../state/me";
import { fmtAge, fmtTimeOfDay, splitLabel } from "../util/time";
import { ScopeList } from "../components/ScopeList";
import { StatusCell } from "../components/StatusCell";
import { CopyId } from "../components/CopyId";
import { decodeAgentRouteID, findAgentForRoute } from "../util/agentRoute";
import type {
  Agent,
  AgentPolicy,
  AgentStorageResolution,
} from "../api/types";

const SYSTEM_SCOPE = "mcp:connect";

function namespaceHints(): string[] {
  const ns = (backends.data.value || [])
    .map((b) => b.namespace || b.id)
    .filter(Boolean);
  return ns.map((n) => `${n}:*`).sort();
}

function visibleScopes(scopes: string[] | undefined): string[] {
  return (scopes || []).filter((s) => s !== SYSTEM_SCOPE);
}

async function setPolicy(prismID: string, p: AgentPolicy) {
  await withToast(async () => {
    await putJSON(`/agents/${encodeURIComponent(prismID)}/policy`, p);
    await agents.refresh();
  });
}

export function AgentDetail() {
  const { params } = useRoute();
  const loc = useLocation();
  const routeID = decodeAgentRouteID(params.prismId);
  const list = agents.data.value || [];
  const agent = findAgentForRoute(list, routeID);

  if (agents.data.value === null) {
    return <Shell title={routeID}>loading…</Shell>;
  }
  if (!agent) {
    return (
      <Shell title={routeID}>
        <div class="empty-state">
          agent not found.{" "}
          <a href="/agents" class="link-accent">
            back to agents
          </a>
        </div>
      </Shell>
    );
  }

  return (
    <div>
      <div class="detail-breadcrumb">
        <a href="/agents">agents</a>
        <span class="breadcrumb-sep">/</span>
        <span class="breadcrumb-current">
          {agent.label || agent.description || agent.client_id}
        </span>
      </div>

      <div class="detail-header">
        <div>
          <div class="page-title">
            {(() => {
              const [name] = splitLabel(
                agent.label || agent.description || agent.client_id,
              );
              return name;
            })()}
          </div>
          <div class="page-subtitle">
            {agent.dynamic ? "dynamic · oauth dcr" : "static · config"} ·{" "}
            <CopyId
              value={agent.prism_id || agent.client_id}
              label={agent.prism_id ? "prism_id" : "client_id"}
            />
          </div>
        </div>
        <div class="detail-status">
          <StatusCell agent={agent} />
        </div>
      </div>

      <MetaRow agent={agent} />
      <PolicySection agent={agent} />
      <StorageResolutionSection agent={agent} />
      <ActivitySection agent={agent} />
      {agent.dynamic && canMutate() && (
        <DangerSection
          agent={agent}
          onRemoved={async () => {
            await agents.refresh();
            loc.route("/agents");
          }}
        />
      )}
    </div>
  );
}

function Shell({
  title,
  children,
}: {
  title: string;
  children: preact.ComponentChildren;
}) {
  return (
    <div>
      <div class="detail-breadcrumb">
        <a href="/agents">agents</a>
        <span class="breadcrumb-sep">/</span>
        <span class="breadcrumb-current">{title}</span>
      </div>
      <div class="page-header">
        <div>
          <div class="page-title">{title}</div>
        </div>
      </div>
      {children}
    </div>
  );
}

function MetaRow({ agent }: { agent: Agent }) {
  const effective = visibleScopes(agent.breakdown?.effective || agent.scopes);
  return (
    <div class="meta-row">
      <MetaItem label="client_id" value={agent.client_id} mono />
      {agent.prism_id && (
        <MetaItem label="prism_id" value={agent.prism_id} mono />
      )}
      <MetaItem
        label="created"
        value={agent.created_at ? fmtAge(agent.created_at) : "—"}
      />
      <MetaItem
        label="last_seen"
        value={agent.last_used_at ? fmtAge(agent.last_used_at) : "never"}
      />
      <MetaItem
        label="effective_scopes"
        value={String(effective.length)}
      />
    </div>
  );
}

function MetaItem({
  label,
  value,
  mono,
}: {
  label: string;
  value: string;
  mono?: boolean;
}) {
  return (
    <div class="meta-item">
      <div class="meta-label">{label}</div>
      <div class={mono ? "meta-value meta-value-mono" : "meta-value"}>
        {value}
      </div>
    </div>
  );
}

function PolicySection({ agent }: { agent: Agent }) {
  const bd = agent.breakdown;
  if (!agent.dynamic) {
    return (
      <div class="section">
        <div class="section-header">
          <span class="section-title">policy</span>
        </div>
        <div class="card">
          <div style="font-family:var(--font-mono);font-size:11px;color:var(--muted)">
            this agent is defined in config. its scopes are managed via the
            config file and cannot be edited from the console.
          </div>
        </div>
      </div>
    );
  }
  if (!agent.prism_id) {
    return (
      <div class="section">
        <div class="section-header">
          <span class="section-title">policy</span>
        </div>
        <div class="card">
          <div style="font-family:var(--font-mono);font-size:11px;color:var(--muted)">
            this dynamic client has not completed oauth consent, so prism has
            not assigned a prism_id yet. policy can be managed after consent;
            the client can still be removed below.
          </div>
        </div>
      </div>
    );
  }
  if (!bd) {
    return (
      <div class="section">
        <div class="section-header">
          <span class="section-title">policy</span>
        </div>
        <div class="empty-state">no policy breakdown available.</div>
      </div>
    );
  }

  const defaultsList = visibleScopes(bd.defaults).sort();
  const effective = visibleScopes(bd.effective).sort();
  const grants = bd.grants || [];
  const denies = bd.denies || [];
  const groupNames = Object.keys(bd.groups || {});
  const policy: AgentPolicy = agent.policy || {
    groups: [],
    grant: [],
    deny: [],
  };
  const prismID = agent.prism_id;
  const allGroups = (groups.data.value || []).map((g) => g.name);

  const updatePolicy = (next: AgentPolicy) => setPolicy(prismID, next);

  const removeGroup = (name: string) =>
    updatePolicy({
      groups: (policy.groups || []).filter((g) => g !== name),
      grant: policy.grant || [],
      deny: policy.deny || [],
    });
  const addGroup = (name: string) =>
    updatePolicy({
      groups: [...(policy.groups || []), name],
      grant: policy.grant || [],
      deny: policy.deny || [],
    });
  const removeGrant = (s: string) =>
    updatePolicy({
      groups: policy.groups || [],
      grant: (policy.grant || []).filter((x) => x !== s),
      deny: policy.deny || [],
    });
  const removeDeny = (s: string) =>
    updatePolicy({
      groups: policy.groups || [],
      grant: policy.grant || [],
      deny: (policy.deny || []).filter((x) => x !== s),
    });
  const addGrant = (s: string) =>
    updatePolicy({
      groups: policy.groups || [],
      grant: [...(policy.grant || []), s],
      deny: policy.deny || [],
    });
  const addDeny = (s: string) =>
    updatePolicy({
      groups: policy.groups || [],
      grant: policy.grant || [],
      deny: [...(policy.deny || []), s],
    });

  const availableGroups = allGroups.filter((n) => !groupNames.includes(n));
  const [groupPicker, setGroupPicker] = useState(false);
  const [grantInput, setGrantInput] = useState(false);
  const [denyInput, setDenyInput] = useState(false);

  return (
    <div class="section">
      <div class="section-header">
        <span class="section-title">policy</span>
        <span class="section-sub">
          effective scopes: {effective.length}
        </span>
      </div>
      <div class="policy-card">
        <div class="policy-row">
          <span class="policy-label">defaults</span>
          <div class="policy-scopes">
            <ScopeList scopes={defaultsList} empty="none" />
          </div>
        </div>

        <div class="policy-row">
          <span class="policy-label">groups</span>
          <div class="policy-scopes">
            {groupNames.map((g) => (
              <span
                class="group-chip"
                key={g}
                title="click to remove"
                onClick={() => removeGroup(g)}
              >
                {g}
              </span>
            ))}
            {groupPicker ? (
              availableGroups.length === 0 ? (
                <span
                  class="hint-text"
                  onClick={() => setGroupPicker(false)}
                >
                  no available groups
                </span>
              ) : (
                <div style="position:relative">
                  <div class="group-dropdown">
                    {availableGroups.map((g) => (
                      <div
                        class="group-dropdown-item"
                        key={g}
                        onMouseDown={(e) => {
                          e.preventDefault();
                          setGroupPicker(false);
                          addGroup(g);
                        }}
                      >
                        {g}
                      </div>
                    ))}
                  </div>
                </div>
              )
            ) : (
              <button class="add-btn" onClick={() => setGroupPicker(true)}>
                + group
              </button>
            )}
          </div>
        </div>

        <div class="policy-row">
          <span class="policy-label">grants</span>
          <div class="policy-scopes">
            {grants.map((s) => (
              <span
                class="scope-tag removable"
                key={s}
                title="click to remove"
                onClick={() => removeGrant(s)}
              >
                {s} <span class="x">x</span>
              </span>
            ))}
            {grantInput ? (
              <ScopeAddInput
                hints={namespaceHints()}
                onSubmit={(s) => {
                  setGrantInput(false);
                  addGrant(s);
                }}
                onCancel={() => setGrantInput(false)}
              />
            ) : (
              <button class="add-btn" onClick={() => setGrantInput(true)}>
                + grant
              </button>
            )}
          </div>
        </div>

        <div class="policy-row">
          <span class="policy-label">denies</span>
          <div class="policy-scopes">
            {denies.map((s) => (
              <span
                class="scope-tag removable denied"
                key={s}
                title="click to remove"
                onClick={() => removeDeny(s)}
              >
                {s} <span class="x">x</span>
              </span>
            ))}
            {denyInput ? (
              <ScopeAddInput
                hints={namespaceHints()}
                onSubmit={(s) => {
                  setDenyInput(false);
                  addDeny(s);
                }}
                onCancel={() => setDenyInput(false)}
              />
            ) : (
              <button class="add-btn" onClick={() => setDenyInput(true)}>
                + deny
              </button>
            )}
          </div>
        </div>

        <div class="policy-row policy-effective">
          <span class="policy-label">effective</span>
          <div class="policy-scopes">
            <ScopeList scopes={effective} empty="none" />
          </div>
        </div>
      </div>
    </div>
  );
}

function ScopeAddInput({
  hints,
  onSubmit,
  onCancel,
}: {
  hints: string[];
  onSubmit: (s: string) => void;
  onCancel: () => void;
}) {
  const [value, setValue] = useState("");
  const matches = (() => {
    const v = value.toLowerCase();
    if (!v) return [];
    return hints.filter((h) => h.toLowerCase().includes(v)).slice(0, 6);
  })();
  return (
    <span class="inline-form" style="display:inline-flex;position:relative">
      <input
        type="text"
        placeholder="scope"
        autoFocus
        style="width:140px"
        value={value}
        spellcheck={false}
        onInput={(e) => setValue((e.target as HTMLInputElement).value)}
        onKeyDown={(e) => {
          if (e.key === "Enter") {
            const v = value.trim();
            if (v) onSubmit(v);
            else onCancel();
          }
          if (e.key === "Escape") onCancel();
        }}
        onBlur={() => setTimeout(onCancel, 150)}
      />
      {matches.length > 0 && (
        <div class="scope-suggest">
          {matches.map((m) => (
            <div
              key={m}
              class="scope-suggest-item"
              onMouseDown={(e) => {
                e.preventDefault();
                onSubmit(m);
              }}
            >
              {m}
            </div>
          ))}
        </div>
      )}
    </span>
  );
}

function ActivitySection({ agent }: { agent: Agent }) {
  const ev = events.data.value || [];
  const scoped = ev.filter((e) => e.client_id === agent.client_id).slice(0, 10);
  const lastCall = scoped[0];

  return (
    <div class="section">
      <div class="section-header">
        <span class="section-title">
          recent activity ({scoped.length})
          {lastCall && (
            <span class="section-sub">· last call {fmtAge(lastCall.ts)}</span>
          )}
        </span>
        <a
          class="section-btn"
          href={`/audit?agent=${encodeURIComponent(agent.client_id)}`}
        >
          view in audit
        </a>
      </div>
      {scoped.length === 0 ? (
        <div class="empty-state">no recent calls from this agent.</div>
      ) : (
        <table class="events-table">
          <thead>
            <tr>
              <th style="width:8%">time</th>
              <th>tool</th>
              <th style="width:8%">status</th>
              <th style="width:8%" class="right">
                latency
              </th>
            </tr>
          </thead>
          <tbody>
            {scoped.map((e, idx) => {
              const latency = e.allowed
                ? e.latency_ms === 0
                  ? "<1ms"
                  : `${e.latency_ms}ms`
                : "-";
              return (
                <tr key={`${e.ts}-${idx}`}>
                  <td class="ev-ts">{fmtTimeOfDay(e.ts)}</td>
                  <td>
                    <span class="ev-tool-ns">{e.namespace}__</span>
                    <span class="ev-tool-name">{e.tool}</span>
                  </td>
                  <td>
                    {e.allowed ? (
                      <span class="ev-status">
                        <span class="dot" />
                      </span>
                    ) : (
                      <span class="ev-status">
                        <span class="denied-text">denied</span>
                      </span>
                    )}
                  </td>
                  <td class="ev-latency">{latency}</td>
                </tr>
              );
            })}
          </tbody>
        </table>
      )}
    </div>
  );
}

function DangerSection({
  agent,
  onRemoved,
}: {
  agent: Agent;
  onRemoved: () => Promise<void>;
}) {
  const [busy, setBusy] = useState(false);

  const remove = async () => {
    if (
      !confirm(
        `Remove agent "${agent.label || agent.client_id}"? Its tokens will be invalidated. Audit history is retained.`,
      )
    )
      return;
    setBusy(true);
    await withToast(async () => {
      await deleteJSON(`/agents/${encodeURIComponent(agent.client_id)}`);
      await onRemoved();
    });
    setBusy(false);
  };

  return (
    <div class="section section-danger">
      <div class="section-header">
        <span class="section-title section-title-danger">danger zone</span>
      </div>
      <div class="card card-danger">
        <div>
          <div class="danger-card-title">remove this agent</div>
          <div class="danger-card-desc">
            invalidates issued tokens immediately. the agent can re-register
            via dcr if it has the credentials. audit log is preserved.
          </div>
        </div>
        <button class="danger-btn" onClick={remove} disabled={busy}>
          {busy ? "removing…" : "remove"}
        </button>
      </div>
    </div>
  );
}


function StorageResolutionSection({ agent }: { agent: Agent }) {
  const [items, setItems] = useState<AgentStorageResolution[] | null>(null);
  const [error, setError] = useState<string | null>(null);

  useEffect(() => {
    if (!agent.prism_id) {
      setItems([]);
      return;
    }
    let cancelled = false;
    (async () => {
      try {
        const data = await getJSON<AgentStorageResolution[]>(
          `/agents/${encodeURIComponent(agent.prism_id!)}/storage-resolution`,
        );
        if (!cancelled) setItems(data);
      } catch (e) {
        if (!cancelled)
          setError(e instanceof Error ? e.message : String(e));
      }
    })();
    return () => {
      cancelled = true;
    };
  }, [agent.prism_id]);

  return (
    <div class="section">
      <div class="section-header">
        <span class="section-title">storage resolution</span>
        <span class="section-sub">
          which workspace each backend would attach to for this agent, with the
          policy layer that decided.
        </span>
      </div>
      {error && <div class="error-text">{error}</div>}
      {items === null ? (
        <div class="empty-state">loading…</div>
      ) : items.length === 0 ? (
        <div class="empty-state">
          no backends registered — nothing to resolve yet.
        </div>
      ) : (
        <div class="card">
          <table class="storage-resolution-table">
            <thead>
              <tr>
                <th>backend</th>
                <th>workspace</th>
                <th>selector</th>
                <th>source</th>
              </tr>
            </thead>
            <tbody>
              {items.map((r) => (
                <tr key={r.backend_id}>
                  <td class="storage-resolution-backend">{r.backend_id}</td>
                  <td>
                    {r.deny_reason ? (
                      <span class="storage-resolution-deny">
                        {r.deny_reason}
                      </span>
                    ) : (
                      r.workspace_id || "—"
                    )}
                  </td>
                  <td>
                    <code>{r.selector}</code>
                  </td>
                  <td class="storage-resolution-source">{r.source}</td>
                </tr>
              ))}
            </tbody>
          </table>
        </div>
      )}
    </div>
  );
}

