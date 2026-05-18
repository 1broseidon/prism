import { useMemo, useState } from "preact/hooks";
import type {
  OpenAPIOperationView,
  OpenAPISkippedOperation,
} from "../api/types";

// OperationPicker is the tag-grouped multi-select used in both the OpenAPI
// add-backend preview pane and in the re-import "added ops" diff view. The
// caller owns the set of enabled operation names; we surface checkbox state
// purely as a function of (operation.name ∈ enabled).
//
// Bulk-toggle per tag is what makes 200-op specs usable — operators routinely
// disable an entire `internal-*` tag in one click.

export interface OperationPickerProps {
  operations: OpenAPIOperationView[];
  enabled: ReadonlySet<string>;
  onChange: (enabled: Set<string>) => void;
  // skipped operations show in a collapsed section below the curation list;
  // operators see why each was dropped without cluttering the picker.
  skipped?: OpenAPISkippedOperation[];
  // specWarnings render in an inline banner above the search input. Parser-
  // emitted strings; we surface them verbatim.
  specWarnings?: string[];
  // Optional title/description shown above the picker; the caller is
  // responsible for the surrounding card chrome.
  title?: string;
  description?: string;
}

interface TagGroup {
  tag: string;
  operations: OpenAPIOperationView[];
}

function groupByTag(ops: OpenAPIOperationView[]): TagGroup[] {
  const groups = new Map<string, OpenAPIOperationView[]>();
  for (const op of ops) {
    // An operation can carry multiple tags. We mirror it into each so the
    // operator can find it under any of its labels; toggling a copy still
    // toggles the underlying name (we dedupe at apply time, not display).
    const tags = op.tags && op.tags.length > 0 ? op.tags : ["(untagged)"];
    for (const tag of tags) {
      const list = groups.get(tag) ?? [];
      list.push(op);
      groups.set(tag, list);
    }
  }
  return Array.from(groups.entries())
    .sort((a, b) => {
      // (untagged) sinks to the bottom so the operator sees meaningful tags
      // first regardless of alphabet.
      if (a[0] === "(untagged)") return 1;
      if (b[0] === "(untagged)") return -1;
      return a[0].localeCompare(b[0]);
    })
    .map(([tag, operations]) => ({ tag, operations }));
}

function matchesQuery(op: OpenAPIOperationView, q: string): boolean {
  if (!q) return true;
  const hay = [
    op.name,
    op.path,
    op.summary || "",
    ...(op.tags || []),
  ]
    .join("\n")
    .toLowerCase();
  return hay.includes(q);
}

export function OperationPicker({
  operations,
  enabled,
  onChange,
  skipped,
  specWarnings,
  title,
  description,
}: OperationPickerProps) {
  const [query, setQuery] = useState("");
  const [collapsed, setCollapsed] = useState<Record<string, boolean>>({});
  const [skippedOpen, setSkippedOpen] = useState(false);

  const q = query.trim().toLowerCase();
  const groups = useMemo(() => groupByTag(operations), [operations]);
  const filteredGroups = useMemo(
    () =>
      groups
        .map((g) => ({
          ...g,
          operations: g.operations.filter((op) => matchesQuery(op, q)),
        }))
        .filter((g) => g.operations.length > 0),
    [groups, q],
  );

  const totalCount = operations.length;
  const enabledCount = operations.reduce(
    (n, op) => n + (enabled.has(op.name) ? 1 : 0),
    0,
  );

  const setNames = (
    names: Iterable<string>,
    on: boolean,
  ): Set<string> => {
    const next = new Set(enabled);
    for (const name of names) {
      if (on) next.add(name);
      else next.delete(name);
    }
    return next;
  };

  const toggleOne = (op: OpenAPIOperationView) => {
    const on = !enabled.has(op.name);
    onChange(setNames([op.name], on));
  };

  const toggleGroup = (group: TagGroup) => {
    const allOn = group.operations.every((op) => enabled.has(op.name));
    onChange(
      setNames(
        group.operations.map((op) => op.name),
        !allOn,
      ),
    );
  };

  const enableAll = () => {
    onChange(new Set(operations.map((op) => op.name)));
  };
  const disableAll = () => {
    onChange(new Set());
  };

  return (
    <div class="op-picker">
      {(title || description) && (
        <div class="op-picker-head">
          {title && <div class="op-picker-title">{title}</div>}
          {description && (
            <div class="op-picker-desc">{description}</div>
          )}
        </div>
      )}

      {specWarnings && specWarnings.length > 0 && (
        <div class="op-picker-warnings" role="alert">
          <div class="op-picker-warnings-title">
            spec warnings ({specWarnings.length})
          </div>
          <ul>
            {specWarnings.map((w) => (
              <li key={w}>{w}</li>
            ))}
          </ul>
        </div>
      )}

      <div class="op-picker-toolbar">
        <div class="op-picker-count">
          <span class="op-picker-count-value">
            {enabledCount}
          </span>
          <span class="op-picker-count-sep">of</span>
          <span class="op-picker-count-value">{totalCount}</span>
          <span class="op-picker-count-label">enabled</span>
        </div>
        <div class="op-picker-bulk">
          <button
            type="button"
            class="section-btn"
            onClick={enableAll}
            disabled={enabledCount === totalCount}
          >
            enable all
          </button>
          <button
            type="button"
            class="section-btn"
            onClick={disableAll}
            disabled={enabledCount === 0}
          >
            disable all
          </button>
        </div>
        <input
          type="search"
          class="search-input op-picker-search"
          placeholder="search tag, name, path, summary…"
          value={query}
          onInput={(e) =>
            setQuery((e.target as HTMLInputElement).value)
          }
        />
      </div>

      {filteredGroups.length === 0 ? (
        <div class="empty-state">
          {operations.length === 0
            ? "no operations parsed from this spec."
            : `no operations match “${query}”.`}
        </div>
      ) : (
        <div class="op-picker-groups">
          {filteredGroups.map((g) => {
            const groupKey = g.tag;
            const groupEnabledCount = g.operations.reduce(
              (n, op) => n + (enabled.has(op.name) ? 1 : 0),
              0,
            );
            const allOn = groupEnabledCount === g.operations.length;
            const noneOn = groupEnabledCount === 0;
            const isCollapsed = collapsed[groupKey] === true;
            return (
              <div class="op-group" key={groupKey}>
                <div class="op-group-header">
                  <button
                    type="button"
                    class="op-group-toggle"
                    onClick={() =>
                      setCollapsed((s) => ({
                        ...s,
                        [groupKey]: !isCollapsed,
                      }))
                    }
                    aria-expanded={!isCollapsed}
                  >
                    <span class="op-group-chevron">
                      {isCollapsed ? "▸" : "▾"}
                    </span>
                    <span class="op-group-tag">{g.tag}</span>
                  </button>
                  <span class="op-group-summary">
                    {groupEnabledCount} of {g.operations.length} enabled
                  </span>
                  <button
                    type="button"
                    class="section-btn op-group-bulk"
                    onClick={() => toggleGroup(g)}
                  >
                    {allOn
                      ? "disable group"
                      : noneOn
                        ? "enable group"
                        : "enable rest"}
                  </button>
                </div>
                {!isCollapsed && (
                  <div class="op-group-rows">
                    {g.operations.map((op) => (
                      <OperationRow
                        key={`${g.tag}:${op.name}`}
                        op={op}
                        enabled={enabled.has(op.name)}
                        onToggle={() => toggleOne(op)}
                      />
                    ))}
                  </div>
                )}
              </div>
            );
          })}
        </div>
      )}

      {skipped && skipped.length > 0 && (
        <div class="op-picker-skipped">
          <button
            type="button"
            class="op-picker-skipped-header"
            onClick={() => setSkippedOpen((v) => !v)}
            aria-expanded={skippedOpen}
          >
            <span class="op-group-chevron">
              {skippedOpen ? "▾" : "▸"}
            </span>
            skipped operations ({skipped.length})
          </button>
          {skippedOpen && (
            <div class="op-picker-skipped-list">
              {skipped.map((s, idx) => (
                <div class="op-skipped-row" key={`${s.name}-${idx}`}>
                  <div class="op-skipped-row-head">
                    {s.method && (
                      <MethodPill method={s.method} />
                    )}
                    <span class="op-skipped-name">{s.name}</span>
                    {s.path && (
                      <span class="op-skipped-path">{s.path}</span>
                    )}
                    <span class="op-skipped-reason">{s.reason}</span>
                  </div>
                  {s.detail && (
                    <div class="op-skipped-detail">{s.detail}</div>
                  )}
                </div>
              ))}
            </div>
          )}
        </div>
      )}
    </div>
  );
}

function OperationRow({
  op,
  enabled,
  onToggle,
}: {
  op: OpenAPIOperationView;
  enabled: boolean;
  onToggle: () => void;
}) {
  return (
    <label
      class={
        enabled ? "op-row" : "op-row op-row-disabled"
      }
    >
      <input
        type="checkbox"
        class="op-row-check"
        checked={enabled}
        onChange={onToggle}
      />
      <MethodPill method={op.method} />
      <span class="op-row-name">{op.name}</span>
      <span class="op-row-path">{op.path}</span>
      {op.deprecated && (
        <span class="op-row-deprecated">deprecated</span>
      )}
      {op.summary && (
        <span class="op-row-summary">{op.summary}</span>
      )}
    </label>
  );
}

export function MethodPill({ method }: { method: string }) {
  const m = (method || "").toUpperCase();
  const cls = m
    ? `method-pill method-pill-${m.toLowerCase()}`
    : "method-pill method-pill-unknown";
  return <span class={cls}>{m || "?"}</span>;
}
