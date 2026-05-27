// CapabilityList — fetches and renders the permission rows for a subject.
//
// Task-46 split the list into two sections:
//
//   ALLOWED  — capability rows with effect="allow" (the default)
//   DENIED   — capability rows with effect="deny" (AgentPolicy.Deny scopes)
//
// ALLOWED renders first so the operator's first scan is "what does this
// subject GET to do?" — denials are an exception layer on top. The DENIED
// section is suppressed entirely when there are no deny rows, except on
// agent subjects where we always render the section with an empty-state
// hint so operators learn the slot exists.
//
// Owns:
//   - the GET /policy/subjects/.../capabilities call
//   - loading / error / empty states
//   - re-fetch after a row is deleted (CapabilityRow handles the DELETE)
//
// Edit dispatch is delegated upward via the `onEdit` prop so the parent
// (SubjectDetail) can wire it to the AddCapabilityModal that ships in
// task-35. This keeps the list pure UI — no router/modal coupling.

import type { ComponentChildren } from "preact";
import { useCallback, useEffect, useMemo, useState } from "preact/hooks";
import type {
  CapabilitySpec,
  CapabilityView,
  SubjectType,
} from "../../api/policy";
import { listCapabilities } from "../../api/policy";
import { showError } from "../../state/toasts";
import { CapabilityRow } from "./CapabilityRow";

export interface CapabilityListProps {
  subjectType: SubjectType;
  subjectID: string;
  /** Display name shown in delete confirmation copy ("from engineering"). */
  subjectLabel?: string;
  /** Bubble edit-row clicks up to a parent that owns the modal. */
  onEdit?: (spec: CapabilitySpec, view: CapabilityView) => void;
}

type LoadState =
  | { status: "loading" }
  | { status: "ready"; rows: CapabilityView[] }
  | { status: "error"; message: string };

export function CapabilityList({
  subjectType,
  subjectID,
  subjectLabel,
  onEdit,
}: CapabilityListProps) {
  const [state, setState] = useState<LoadState>({ status: "loading" });

  const load = useCallback(async () => {
    setState({ status: "loading" });
    try {
      const rows = await listCapabilities(subjectType, subjectID);
      setState({ status: "ready", rows });
    } catch (err) {
      const message = err instanceof Error ? err.message : String(err);
      setState({ status: "error", message });
    }
  }, [subjectType, subjectID]);

  useEffect(() => {
    void load();
  }, [load]);

  const handleDelete = useCallback(
    (deletedID: string) => {
      setState((prev) =>
        prev.status === "ready"
          ? {
              status: "ready",
              rows: prev.rows.filter((r) => r.id !== deletedID),
            }
          : prev,
      );
    },
    [],
  );

  const handleError = useCallback((err: unknown) => {
    showError(err instanceof Error ? err.message : String(err));
  }, []);

  // Group by effect once per load. Rows without an explicit effect default
  // to "allow" — older responses (pre-task-46) didn't carry the field.
  const grouped = useMemo(() => {
    const allowed: CapabilityView[] = [];
    const denied: CapabilityView[] = [];
    if (state.status === "ready") {
      for (const r of state.rows) {
        if (r.effect === "deny") denied.push(r);
        else allowed.push(r);
      }
    }
    return { allowed, denied };
  }, [state]);

  if (state.status === "loading") {
    return <CapabilityListSkeleton />;
  }
  if (state.status === "error") {
    return (
      <div class="capability-list-error" role="alert">
        <div class="capability-list-error-msg">
          could not load permissions: {state.message}
        </div>
        <button
          type="button"
          class="section-btn"
          onClick={() => void load()}
        >
          retry
        </button>
      </div>
    );
  }
  if (state.rows.length === 0) {
    return <CapabilityListEmpty />;
  }

  // Agent subjects always show the DENIED section header (even when empty)
  // so operators learn the slot exists and where deny rules render.
  const showDeniedSection =
    grouped.denied.length > 0 || subjectType === "agents";

  const renderRow = (row: CapabilityView) => (
    <CapabilityRow
      key={row.id}
      view={row}
      subjectType={subjectType}
      subjectID={subjectID}
      subjectLabel={subjectLabel}
      onEdit={onEdit}
      onDelete={handleDelete}
      onError={handleError}
    />
  );

  return (
    <div class="capability-list-sections">
      <CapabilitySection
        kind="allow"
        title="ALLOWED"
        count={grouped.allowed.length}
      >
        {grouped.allowed.length > 0 ? (
          <div class="capability-list" role="list">
            {grouped.allowed.map(renderRow)}
          </div>
        ) : (
          <div class="capability-section-empty">
            No allow rules for this subject.
          </div>
        )}
      </CapabilitySection>

      {showDeniedSection && (
        <CapabilitySection
          kind="deny"
          title="DENIED"
          count={grouped.denied.length}
        >
          {grouped.denied.length > 0 ? (
            <div class="capability-list" role="list">
              {grouped.denied.map(renderRow)}
            </div>
          ) : (
            <div class="capability-section-empty">
              No explicit deny rules. Add one to override an inherited allow.
            </div>
          )}
        </CapabilitySection>
      )}
    </div>
  );
}

interface SectionProps {
  kind: "allow" | "deny";
  title: string;
  count: number;
  children: ComponentChildren;
}

function CapabilitySection({ kind, title, count, children }: SectionProps) {
  return (
    <section class={`capability-section capability-section-${kind}`}>
      <header class="capability-section-head">
        <span class="capability-section-title">{title}</span>
        <span class="capability-section-count" aria-label={`${count} rules`}>
          {count}
        </span>
      </header>
      <div class="capability-section-body">{children}</div>
    </section>
  );
}

function CapabilityListSkeleton() {
  // Three shimmer rows mirrors the visual rhythm of the real list and the
  // skeleton-scan animation already shipped for backend cards (app.css).
  return (
    <div class="capability-list capability-list-skeleton" aria-busy="true">
      {[0, 1, 2].map((i) => (
        <div class="capability-row capability-row-skeleton" key={i}>
          <div class="capability-row-main">
            <span class="capability-skeleton-bar capability-skeleton-bar-sm" />
            <span class="capability-skeleton-bar capability-skeleton-bar-md" />
            <span class="capability-skeleton-bar capability-skeleton-bar-lg" />
          </div>
        </div>
      ))}
    </div>
  );
}

function CapabilityListEmpty() {
  return (
    <div class="capability-list-empty">
      <div class="capability-list-empty-arrow" aria-hidden="true">↑</div>
      <div class="capability-list-empty-text">
        No permissions yet. Click <strong>Add permission</strong> to get started.
      </div>
    </div>
  );
}
