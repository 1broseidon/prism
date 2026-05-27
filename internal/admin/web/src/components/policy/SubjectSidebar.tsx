// SubjectSidebar — the left-rail subject picker for the /policy hub.
//
// Two collapsible sections — Groups and Roles — per spec §4.2 as refined in
// the policy-refine rework (task-38). Individual agents are intentionally
// NOT listed here: operators authorize TEAMS (groups + roles), and reach
// individual agents through the dedicated /agents page for audit + the
// per-agent Direct Grants escape hatch. A small "Agents page →" link at
// the bottom of the sidebar makes that handoff explicit without re-adding
// the (long) per-agent list.
//
// The Roles section sources its names from the union of bindings'
// SubjectSelector.Roles fields exposed via the GrantBinding list — task-32
// (policy backend) made roles addressable on the API but the listing
// surface isn't a single endpoint yet. To avoid a new endpoint round-trip
// in this task, we derive role names from the agent policy snapshots
// (which embed group memberships and any explicit role tags) plus a
// well-known builtin list. The list is best-effort — operators can always
// add a role by typing it in the "+ Add role" inline form.

import type { ComponentChildren } from "preact";
import { useEffect, useMemo, useRef, useState } from "preact/hooks";
import type { Signal } from "@preact/signals";
import { groups } from "../../state";
import { canMutate } from "../../state/me";
import { putJSON } from "../../api/client";
import { withToast } from "../../state/toasts";
import { listGrantBindings, type GrantBinding } from "../../api/grants";
import { AdvancedToggle } from "./AdvancedToggle";
import { useAdvanced } from "../../hooks/useAdvanced";
import type { Group } from "../../api/types";

// Advanced sub-nav links rendered when the toggle is ON. All three routes
// live under /policy/advanced/* now — the legacy /grants/* paths still
// resolve via redirects in app.tsx for any saved bookmarks.
const ADVANCED_LINKS: Array<{ href: string; label: string }> = [
  { href: "/policy/advanced", label: "Cross-cutting view" },
  { href: "/policy/advanced/templates", label: "Templates" },
  { href: "/policy/advanced/bindings", label: "Bindings" },
];

interface Props {
  // Current path so we can highlight the active subject without re-deriving
  // it from window.location.
  activePath: string;
}

export function SubjectSidebar({ activePath }: Props) {
  const groupList = useGroups();
  const roleList = useRoles();

  return (
    <aside class="policy-sidebar" aria-label="Subjects">
      <div class="policy-sidebar-heading">Subjects</div>

      <GroupsSection groups={groupList} activePath={activePath} />
      <RolesSection roles={roleList} activePath={activePath} />

      <div class="policy-sidebar-divider" />

      <a class="policy-sidebar-agents-link" href="/agents/members">
        Agents page →
      </a>

      <div class="policy-sidebar-footer">
        <AdvancedToggle />
      </div>

      <AdvancedNav />
    </aside>
  );
}

// ── Section: Groups ─────────────────────────────────────────────────────────

function GroupsSection({
  groups: gr,
  activePath,
}: {
  groups: Group[];
  activePath: string;
}) {
  const [open, setOpen] = useState(true);
  const [adding, setAdding] = useState(false);
  const mutate = canMutate();

  const submitName = async (raw: string) => {
    const name = raw.trim();
    if (!name) return false;
    const ok = await withToast(async () => {
      await putJSON(`/groups/${encodeURIComponent(name)}`, { scopes: [] });
      await groups.refresh();
    });
    return ok !== undefined;
  };

  return (
    <Section
      title="Groups"
      count={gr.length}
      open={open}
      onToggle={() => setOpen((v) => !v)}
    >
      <ul class="policy-sidebar-list" role="list">
        {gr.map((g) => (
          <SubjectLink
            key={g.id || g.name}
            href={`/policy/groups/${encodeURIComponent(g.id || g.name)}`}
            label={g.display_name || g.name}
            activePath={activePath}
          />
        ))}
      </ul>
      {gr.length === 0 && (
        <div class="policy-sidebar-empty">no groups yet</div>
      )}
      {mutate && (
        <AddRow
          adding={adding}
          onStart={() => setAdding(true)}
          onCancel={() => setAdding(false)}
          onSubmit={async (n) => {
            const ok = await submitName(n);
            if (ok) setAdding(false);
            return ok;
          }}
          ctaLabel="+ Add group"
          placeholder="group name"
        />
      )}
    </Section>
  );
}

// ── Section: Roles ──────────────────────────────────────────────────────────

function RolesSection({
  roles,
  activePath,
}: {
  roles: string[];
  activePath: string;
}) {
  const [open, setOpen] = useState(true);
  const [adding, setAdding] = useState(false);
  const [extras, setExtras] = useState<string[]>([]);
  const mutate = canMutate();

  // Operators can add a role name locally even before any binding exists —
  // they need a way to navigate to /policy/roles/:name to start authoring.
  // The "real" role list comes from binding aggregation; this list holds
  // anything typed in via the inline form so the just-added name stays
  // visible until a binding cements it.
  const merged = useMemo(() => {
    const seen = new Set<string>();
    const out: string[] = [];
    for (const name of [...roles, ...extras]) {
      if (seen.has(name)) continue;
      seen.add(name);
      out.push(name);
    }
    return out.sort();
  }, [roles, extras]);

  return (
    <Section
      title="Roles"
      count={merged.length}
      open={open}
      onToggle={() => setOpen((v) => !v)}
    >
      <ul class="policy-sidebar-list" role="list">
        {merged.map((r) => (
          <SubjectLink
            key={r}
            href={`/policy/roles/${encodeURIComponent(r)}`}
            label={r}
            activePath={activePath}
          />
        ))}
      </ul>
      {merged.length === 0 && (
        <div class="policy-sidebar-empty">no roles in use</div>
      )}
      {mutate && (
        <AddRow
          adding={adding}
          onStart={() => setAdding(true)}
          onCancel={() => setAdding(false)}
          onSubmit={async (n) => {
            const trimmed = n.trim();
            if (!trimmed) return false;
            setExtras((prev) =>
              prev.includes(trimmed) ? prev : [...prev, trimmed],
            );
            setAdding(false);
            return true;
          }}
          ctaLabel="+ Add role"
          placeholder="role name"
        />
      )}
    </Section>
  );
}

// ── Advanced sub-nav (rendered when toggle ON) ──────────────────────────────

function AdvancedNav() {
  const on = useAdvanced();
  if (!on) return null;
  return (
    <div class="policy-sidebar-advanced-nav" aria-label="Advanced">
      <div class="policy-sidebar-heading">Advanced</div>
      <ul class="policy-sidebar-list" role="list">
        {ADVANCED_LINKS.map((l) => (
          <li key={l.href}>
            <a class="policy-sidebar-link" href={l.href}>
              {l.label}
            </a>
          </li>
        ))}
      </ul>
    </div>
  );
}

// ── Primitive building blocks ───────────────────────────────────────────────

function Section({
  title,
  count,
  open,
  onToggle,
  children,
}: {
  title: string;
  count: number;
  open: boolean;
  onToggle: () => void;
  children: ComponentChildren;
}) {
  return (
    <div class={open ? "policy-sidebar-section open" : "policy-sidebar-section"}>
      <button
        type="button"
        class="policy-sidebar-section-head"
        aria-expanded={open}
        onClick={onToggle}
      >
        <span class="policy-sidebar-section-caret" aria-hidden="true">
          {open ? "▾" : "▸"}
        </span>
        <span class="policy-sidebar-section-title">{title}</span>
        <span class="policy-sidebar-section-count">{count}</span>
      </button>
      {open && <div class="policy-sidebar-section-body">{children}</div>}
    </div>
  );
}

function SubjectLink({
  href,
  label,
  activePath,
}: {
  href: string;
  label: string;
  activePath: string;
}) {
  // Strict-equality on the path keeps a group named "agents" from
  // accidentally lighting up when the operator visits /policy/agents/foo.
  const active = activePath === href;
  return (
    <li>
      <a
        href={href}
        class={active ? "policy-sidebar-link active" : "policy-sidebar-link"}
        aria-current={active ? "page" : undefined}
      >
        {label}
      </a>
    </li>
  );
}

function AddRow({
  adding,
  onStart,
  onCancel,
  onSubmit,
  ctaLabel,
  placeholder,
}: {
  adding: boolean;
  onStart: () => void;
  onCancel: () => void;
  onSubmit: (raw: string) => Promise<boolean>;
  ctaLabel: string;
  placeholder: string;
}) {
  const [value, setValue] = useState("");
  const inputRef = useRef<HTMLInputElement | null>(null);

  useEffect(() => {
    if (adding) {
      setValue("");
      inputRef.current?.focus();
    }
  }, [adding]);

  if (!adding) {
    return (
      <button class="policy-sidebar-add" type="button" onClick={onStart}>
        {ctaLabel}
      </button>
    );
  }

  const submit = async () => {
    if (!value.trim()) {
      onCancel();
      return;
    }
    await onSubmit(value);
  };

  return (
    <div class="policy-sidebar-add-form">
      <input
        ref={inputRef}
        type="text"
        class="policy-sidebar-add-input"
        placeholder={placeholder}
        value={value}
        spellcheck={false}
        onInput={(e) => setValue((e.target as HTMLInputElement).value)}
        onKeyDown={(e) => {
          if (e.key === "Enter") submit();
          if (e.key === "Escape") onCancel();
        }}
      />
    </div>
  );
}

// ── Data sources ────────────────────────────────────────────────────────────

function useGroups(): Group[] {
  return useSignalSorted(groups.data, (a, b) => a.name.localeCompare(b.name));
}

// useRoles aggregates role names from two sources:
//   1. Roles referenced via SubjectSelector.role_required on existing
//      bindings (best evidence the role is "in use").
//   2. Roles attached to groups via dynamic role tagging — none today, but
//      reserved for forward-compat.
// Failures are swallowed; an empty Roles section is correct behavior on a
// fresh install.
function useRoles(): string[] {
  const [roles, setRoles] = useState<string[]>([]);
  useEffect(() => {
    let cancelled = false;
    (async () => {
      try {
        const bindings = await listGrantBindings();
        const seen = new Set<string>();
        for (const b of bindings as GrantBinding[]) {
          const list = b.subjects?.roles || [];
          for (const r of list) seen.add(r);
          if (b.subjects?.role_required) seen.add(b.subjects.role_required);
        }
        if (!cancelled) setRoles(Array.from(seen).sort());
      } catch {
        if (!cancelled) setRoles([]);
      }
    })();
    return () => {
      cancelled = true;
    };
  }, []);
  return roles;
}

function useSignalSorted<T>(
  src: Signal<T[] | null>,
  cmp: (a: T, b: T) => number,
): T[] {
  // Reading .value subscribes the consuming component to changes.
  const v = src.value;
  return useMemo(() => (v ? [...v].sort(cmp) : []), [v]);
}
