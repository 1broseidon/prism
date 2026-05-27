// AgentDirectGrants — per-agent direct-grant escape hatch (spec §7.3).
//
// Most per-agent capability authoring is intentionally read-only — operators
// edit groups and roles, and the agent inherits. The escape hatch lives
// here for the (rare) case where a single agent legitimately needs a
// one-off capability that isn't worth a new group. Per the policy-refine
// rework (task-38) this page is NOT Advanced-gated: any admin can use it
// as a normal workflow. The Advanced concept is reserved for surfacing the
// raw template/binding plumbing under /policy/advanced/*.
//
// Layout mirrors the SubjectDetail shell: sidebar + main, capability list +
// add modal. Subject type is "agents" so the API calls hit
// /admin/policy/subjects/agents/{prismId}/capabilities.

import { useCallback, useEffect, useState } from "preact/hooks";
import { useLocation, useRoute } from "preact-iso";
import { SubjectSidebar } from "../components/policy/SubjectSidebar";
import { CapabilityList } from "../components/policy/CapabilityList";
import {
  AddCapabilityModal,
  type CapabilityModalMode,
} from "../components/policy/AddCapabilityModal";
import {
  listCapabilities,
  type CapabilitySpec,
  type CapabilityView,
} from "../api/policy";
import { ApiError } from "../api/client";
import { agents } from "../state";
import { decodeAgentRouteID, findAgentForRoute } from "../util/agentRoute";

interface RouteParams {
  prismId?: string;
}

export function AgentDirectGrants() {
  const { params } = useRoute();
  const loc = useLocation();
  const p = params as unknown as RouteParams;
  const routeID = decodeAgentRouteID(p.prismId);
  const agentList = agents.data.value || [];
  const agent = findAgentForRoute(agentList, routeID);
  const subjectID = agent?.prism_id || routeID;

  const [, setCaps] = useState<CapabilityView[] | null>(null);
  const [error, setError] = useState<string | null>(null);
  const [listToken, setListToken] = useState(0);
  const [modal, setModal] = useState<
    | null
    | {
        mode: CapabilityModalMode;
        spec?: CapabilitySpec;
        view?: CapabilityView;
      }
  >(null);

  const refetch = useCallback(async () => {
    if (!subjectID) return;
    try {
      const list = await listCapabilities("agents", subjectID);
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
  }, [subjectID]);

  useEffect(() => {
    if (!subjectID) return;
    void refetch();
  }, [subjectID, refetch]);

  const label = agent?.label || agent?.description || subjectID || "agent";

  return (
    <div class="policy-shell">
      <SubjectSidebar activePath={loc.path || "/policy"} />
      <div class="policy-main">
        <div class="detail-breadcrumb">
          <a href="/agents">agents</a>
          <span class="breadcrumb-sep">/</span>
          <a href={`/policy/agents/${encodeURIComponent(routeID)}`}>{label}</a>
          <span class="breadcrumb-sep">/</span>
          <span class="breadcrumb-current">direct permissions</span>
        </div>

        <div class="page-header">
          <div>
            <div class="page-title">Direct permissions for {label}</div>
            <div class="page-subtitle">
              Per-agent escape hatch. Prefer editing a group or role — direct
              permissions drift from your policy fleet over time.
            </div>
          </div>
        </div>

        {!subjectID ? (
          <div class="empty-state">
            agent not found —{" "}
            <a class="link-accent" href="/agents">
              back to agents
            </a>
            .
          </div>
        ) : (
          <section
            class="policy-capabilities"
            aria-label="Direct permissions"
          >
            <header class="policy-capabilities-head">
              <h2 class="policy-capabilities-title">Direct permissions</h2>
              <button
                type="button"
                class="policy-capabilities-add"
                onClick={() => setModal({ mode: "add" })}
              >
                + Add direct permission
              </button>
            </header>
            {error && (
              <div class="policy-capabilities-error">
                Failed to load: {error}
              </div>
            )}
            <CapabilityList
              key={listToken}
              subjectType="agents"
              subjectID={subjectID}
              subjectLabel={label}
              onEdit={(spec, view) => setModal({ mode: "edit", spec, view })}
            />
          </section>
        )}

        {modal && subjectID && (
          <AddCapabilityModal
            mode={modal.mode}
            subjectType="agents"
            subjectID={subjectID}
            subjectLabel={label}
            initialSpec={modal.spec}
            initialView={modal.view}
            onCancel={() => setModal(null)}
            onSaved={() => {
              setModal(null);
              void refetch();
            }}
          />
        )}
      </div>
    </div>
  );
}
