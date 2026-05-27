// SubjectDetail — generic per-subject capabilities page.
//
// Renders the policy shell:  sidebar + summary header + capabilities body.
// Capability rows come from CapabilityList (task-34); the "+ Add capability"
// button and row-edit clicks open the AddCapabilityModal (task-35).

import { useCallback, useEffect, useState } from "preact/hooks";
import { useLocation, useRoute } from "preact-iso";
import { SubjectSidebar } from "../components/policy/SubjectSidebar";
import { SubjectHeader } from "../components/policy/SubjectHeader";
import { CapabilityList } from "../components/policy/CapabilityList";
import { PolicyHealthStrip } from "../components/policy/PolicyHealthStrip";
import {
  AddCapabilityModal,
  type CapabilityModalMode,
} from "../components/policy/AddCapabilityModal";
import {
  listCapabilities,
  type CapabilitySpec,
  type CapabilityView,
  type SubjectType,
} from "../api/policy";
import { ApiError } from "../api/client";

// Persisted across navigations so /policy can redirect to the last subject.
export const LAST_SUBJECT_STORAGE_KEY = "prism.policy.last_subject";

interface RouteParams {
  // Each subject route uses :name (groups/roles) or :prismId (agents); the
  // sidebar and detail page accept either via this single field.
  name?: string;
  prismId?: string;
}

interface Props {
  subjectType: SubjectType;
}

export function SubjectDetail({ subjectType }: Props) {
  const { params } = useRoute();
  const loc = useLocation();
  const p = params as unknown as RouteParams;
  const rawID = subjectType === "agents" ? p.prismId : p.name;
  const subjectID = rawID ? decodeURIComponent(rawID) : "";

  const [caps, setCaps] = useState<CapabilityView[] | null>(null);
  const [error, setError] = useState<string | null>(null);

  // Modal state. mode === null → modal closed; otherwise we render the
  // AddCapabilityModal with the appropriate mode + initial values.
  const [modal, setModal] = useState<
    | null
    | {
        mode: CapabilityModalMode;
        spec?: CapabilitySpec;
        view?: CapabilityView;
      }
  >(null);

  // Re-fetch the capability list after a save closes the modal. CapabilityList
  // owns its own internal fetch, but we mirror the list here for the header
  // counts so we have to nudge it ourselves; bumping listToken does the trick
  // by remounting the list when needed.
  const [listToken, setListToken] = useState(0);
  const refetchAll = useCallback(async () => {
    if (!subjectID) return;
    try {
      const list = await listCapabilities(subjectType, subjectID);
      setCaps(list);
      setListToken((n) => n + 1);
    } catch (e) {
      if (e instanceof ApiError && e.status === 404) {
        setCaps([]);
        setListToken((n) => n + 1);
        return;
      }
      setError(e instanceof Error ? e.message : String(e));
    }
  }, [subjectType, subjectID]);

  // Persist the latest visit so /policy can route back to it on next load.
  // Only persist policy subjects (groups/roles). Agents are managed at
  // /agents/* (task-38) and never round-trip through /policy.
  useEffect(() => {
    if (!subjectID) return;
    if (subjectType !== "groups" && subjectType !== "roles") return;
    try {
      window.localStorage.setItem(
        LAST_SUBJECT_STORAGE_KEY,
        `${subjectType}/${encodeURIComponent(subjectID)}`,
      );
    } catch {
      // localStorage can throw in privacy modes — non-fatal, just skip.
    }
  }, [subjectType, subjectID]);

  useEffect(() => {
    if (!subjectID) {
      setCaps([]);
      return;
    }
    let cancelled = false;
    setCaps(null);
    setError(null);
    (async () => {
      try {
        const list = await listCapabilities(subjectType, subjectID);
        if (!cancelled) setCaps(list);
      } catch (e) {
        if (cancelled) return;
        // 404 from the backend is "subject has no capabilities yet" for
        // groups/roles that exist only as a name — treat as empty list to
        // keep the UI usable.
        if (e instanceof ApiError && e.status === 404) {
          setCaps([]);
        } else {
          setError(e instanceof Error ? e.message : String(e));
          setCaps([]);
        }
      }
    })();
    return () => {
      cancelled = true;
    };
  }, [subjectType, subjectID]);

  const activePath = loc.path || "/policy";

  return (
    <>
      {/* Health strip sits above the sidebar split so the six glanceable
          numbers are the first thing the operator sees on /policy. It owns
          its own error surface and never blocks the rest of the page —
          spec: task-41. */}
      <PolicyHealthStrip />
      <div class="policy-shell">
        <SubjectSidebar activePath={activePath} />

        <div class="policy-main">
          {!subjectID ? (
            <EmptyHero />
          ) : (
            <>
              {subjectType === "agents" && (
                <AgentReadOnlyBanner subjectID={subjectID} />
              )}
              <SubjectHeader
                subjectType={subjectType}
                subjectID={subjectID}
                capabilities={caps || []}
              />
              <section
                class="policy-capabilities"
                aria-label="Permissions"
              >
                <header class="policy-capabilities-head">
                  <h2 class="policy-capabilities-title">Permissions</h2>
                  <button
                    type="button"
                    class="policy-capabilities-add"
                    disabled={subjectType === "agents"}
                    title={
                      subjectType === "agents"
                        ? "edit at the group level"
                        : undefined
                    }
                    onClick={() => setModal({ mode: "add" })}
                  >
                    + Add permission
                  </button>
                </header>
                {error && (
                  <div class="policy-capabilities-error">
                    Failed to load: {error}
                  </div>
                )}
                <CapabilityList
                  key={listToken}
                  subjectType={subjectType}
                  subjectID={subjectID}
                  subjectLabel={subjectID}
                  onEdit={(spec, view) =>
                    setModal({ mode: "edit", spec, view })
                  }
                />
              </section>
              {modal && (
                <AddCapabilityModal
                  mode={modal.mode}
                  subjectType={subjectType}
                  subjectID={subjectID}
                  subjectLabel={subjectID}
                  initialSpec={modal.spec}
                  initialView={modal.view}
                  onCancel={() => setModal(null)}
                  onSaved={() => {
                    setModal(null);
                    void refetchAll();
                  }}
                />
              )}
            </>
          )}
        </div>
      </div>
    </>
  );
}

function EmptyHero() {
  return (
    <div class="policy-empty-hero">
      <div class="policy-empty-title">Pick a subject</div>
      <div class="policy-empty-body">
        Choose a group, role, or agent from the sidebar to view and edit its
        permissions.
      </div>
    </div>
  );
}

function AgentReadOnlyBanner({ subjectID }: { subjectID: string }) {
  // Spec §7.3: per-agent permission authoring is read-only. The "inherited
  // from group: …" detail is computed in task-34 from the agent's group
  // memberships; here we just render the warning chip.
  return (
    <div class="policy-readonly-banner" role="note">
      <span class="policy-readonly-badge">Read-only</span>
      Per-agent permissions are inherited from groups and roles. Edit them at
      the group level. Agent: <code>{subjectID}</code>
    </div>
  );
}

// PolicyRedirect — handles GET /policy by routing the operator to the last
// subject they visited (per spec §4.1). Falls through to /policy/groups
// when no history is present. Only honors groups/roles — agents are no
// longer policy subjects (task-38), so any stale agent value is cleared.
export function PolicyRedirect() {
  const loc = useLocation();
  useEffect(() => {
    let dest = "/policy/groups";
    try {
      const last = window.localStorage.getItem(LAST_SUBJECT_STORAGE_KEY);
      if (last && (last.startsWith("groups/") || last.startsWith("roles/"))) {
        dest = `/policy/${last}`;
      } else if (last) {
        window.localStorage.removeItem(LAST_SUBJECT_STORAGE_KEY);
      }
    } catch {
      // ignore; default redirect still works
    }
    loc.route(dest, true);
  }, []);
  return <div class="policy-shell" />;
}

// PolicyGroupsIndex — empty landing when no groups exist (or when the
// operator types /policy/groups manually). Renders the shell + the "pick a
// subject" hero so the page is never blank.
export function PolicyGroupsIndex() {
  return <SubjectDetail subjectType="groups" />;
}

// (PowerToolsCrossCut was a placeholder shipped in task-33. The real
// cross-cutting view lives in pages/Advanced.tsx now, wired in app.tsx
// behind the AdvancedGuard.)
