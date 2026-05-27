// WhyTraceModal — per-row "Why?" popover that explains exactly where a
// capability came from for an agent. Triggered by the `[?]` button on each
// Effective Policy row (CapabilityReadRow / CapabilityRow).
//
// Spec context: task-39 folds the standalone PolicyResolutionSection into a
// per-row affordance. That section previously surfaced the full backend ×
// policy resolution table; for the agent page the operator really wants the
// answer to "why does this row exist?" — i.e. the inheritance chain — and,
// when relevant, the backend resolution layer that decided the actual
// workspace + rate limit.
//
// This component is PRESENTATIONAL — it does not fetch. AgentDetail already
// pulls AgentPolicyResolution[] for the agent and passes it through. We
// render the inheritance chain from CapabilityView.inherited_from and,
// if a backend-specific resolution is relevant, the matching row.

import { useEffect } from "preact/hooks";
import type { CapabilityView } from "../../api/policy";
import type { AgentPolicyResolution } from "../../api/types";

interface Props {
  open: boolean;
  onClose: () => void;
  view: CapabilityView;
  prismID: string;
  /** All backend resolutions for the agent (already fetched by AgentDetail). */
  resolutions?: AgentPolicyResolution[];
}

export function WhyTraceModal({
  open,
  onClose,
  view,
  prismID,
  resolutions,
}: Props) {
  useEffect(() => {
    if (!open) return;
    const onKey = (e: KeyboardEvent) => {
      if (e.key === "Escape") onClose();
    };
    window.addEventListener("keydown", onKey);
    return () => window.removeEventListener("keydown", onKey);
  }, [open, onClose]);

  if (!open) return null;

  const inherited = view.inherited_from || [];
  const backendID = inferBackend(view);
  const relevantResolution = backendID
    ? (resolutions || []).find((r) => r.backend_id === backendID)
    : undefined;

  return (
    <div
      class="modal-backdrop"
      onClick={(e) => {
        if (e.target === e.currentTarget) onClose();
      }}
    >
      <div
        class="modal-card why-modal"
        role="dialog"
        aria-modal="true"
        aria-label="why does this permission resolve"
      >
        <div class="modal-header">
          <div class="modal-title">why does this resolve?</div>
          <div class="modal-sub">
            {view.display_summary || `[${view.id}]`}
          </div>
          <button
            type="button"
            class="modal-close"
            onClick={onClose}
            aria-label="close"
          >
            ×
          </button>
        </div>
        <div class="modal-body">
          <div class="why-section">
            <div class="why-section-label">inheritance chain</div>
            {inherited.length === 0 ? (
              <div class="empty-state">
                this permission is attached directly to the agent (no group or
                role contributed it).
              </div>
            ) : (
              <ol class="why-chain">
                {inherited.map((src, i) => (
                  <li key={`${src.type}-${src.name}-${i}`}>
                    <span class={`why-chain-kind why-chain-kind-${src.type}`}>
                      {src.type}
                    </span>
                    <a class="link-accent" href={sourceHref(src.type, src.name, prismID)}>
                      {src.name || "(direct permission)"}
                    </a>
                  </li>
                ))}
              </ol>
            )}
          </div>

          {relevantResolution && (
            <div class="why-section">
              <div class="why-section-label">
                backend resolution for{" "}
                <code>{relevantResolution.backend_id}</code>
              </div>
              <table class="why-resolution-table">
                <tbody>
                  <tr>
                    <th>workspace</th>
                    <td>
                      {relevantResolution.workspace?.deny_reason ? (
                        <span class="storage-resolution-deny">
                          {relevantResolution.workspace.deny_reason}
                        </span>
                      ) : (
                        relevantResolution.workspace?.workspace_id || "—"
                      )}
                    </td>
                    <td class="why-resolution-source">
                      via {relevantResolution.workspace?.source || "—"}
                    </td>
                  </tr>
                  <tr>
                    <th>selector</th>
                    <td colSpan={2}>
                      {relevantResolution.workspace?.selector ? (
                        <code>{relevantResolution.workspace.selector}</code>
                      ) : (
                        "—"
                      )}
                    </td>
                  </tr>
                  <tr>
                    <th>rate limit</th>
                    <td>
                      {relevantResolution.rate_limit?.rps
                        ? `${relevantResolution.rate_limit.rps} rps${
                            relevantResolution.rate_limit.burst
                              ? ` · burst ${relevantResolution.rate_limit.burst}`
                              : ""
                          }`
                        : "unlimited"}
                    </td>
                    <td class="why-resolution-source">
                      via {relevantResolution.rate_limit?.source || "—"}
                    </td>
                  </tr>
                </tbody>
              </table>
              {(relevantResolution.workspace?.layers?.length ||
                relevantResolution.rate_limit?.layers?.length) && (
                <details class="why-layers">
                  <summary>full layer stack</summary>
                  <div class="why-layers-grid">
                    {relevantResolution.workspace?.layers &&
                      relevantResolution.workspace.layers.length > 0 && (
                        <div>
                          <div class="why-layers-head">workspace layers</div>
                          <ul>
                            {relevantResolution.workspace.layers.map((l, i) => (
                              <li key={`ws-${i}`}>
                                <code>{l.source}</code>
                                {l.selector ? ` → ${l.selector}` : ""}
                              </li>
                            ))}
                          </ul>
                        </div>
                      )}
                    {relevantResolution.rate_limit?.layers &&
                      relevantResolution.rate_limit.layers.length > 0 && (
                        <div>
                          <div class="why-layers-head">rate-limit layers</div>
                          <ul>
                            {relevantResolution.rate_limit.layers.map(
                              (l, i) => (
                                <li key={`rl-${i}`}>
                                  <code>{l.source}</code>
                                  {l.selector ? ` → ${l.selector}` : ""}
                                </li>
                              ),
                            )}
                          </ul>
                        </div>
                      )}
                  </div>
                </details>
              )}
            </div>
          )}

          <div class="why-section why-footer">
            <span class="hint-text">
              source: <code>/admin/policy/subjects/agents/{prismID}/capabilities</code>{" "}
              (inheritance chain) and{" "}
              <code>/admin/agents/{prismID}/policy-resolution</code> (backend
              layers). Both reads, no new endpoints.
            </span>
          </div>
        </div>
      </div>
    </div>
  );
}

function sourceHref(
  kind: "group" | "role" | "direct",
  name: string | undefined,
  prismID: string,
): string {
  switch (kind) {
    case "group":
      return `/policy/groups/${encodeURIComponent(name || "")}`;
    case "role":
      return `/policy/roles/${encodeURIComponent(name || "")}`;
    case "direct":
      return `/policy/agents/${encodeURIComponent(prismID)}/direct-grants`;
  }
}

// inferBackend pulls the backend ID out of a capability spec when the row is
// scoped to a single backend — we use that to highlight the matching layer
// in the resolution table. When the action is a verb or a wildcard we leave
// it undefined (no single backend to focus on).
function inferBackend(view: CapabilityView): string | undefined {
  const action = view.spec?.action;
  if (!action) return undefined;
  if (action.mode === "tool" || action.mode === "backend_wildcard") {
    return action.backend || undefined;
  }
  return undefined;
}
