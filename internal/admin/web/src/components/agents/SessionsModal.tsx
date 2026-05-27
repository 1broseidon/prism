// SessionsModal — surfaces the agent's live tokens as a focused modal
// triggered from the sticky-header "Sessions" badge.
//
// Spec context: task-39 — Sessions becomes a header-level glance + modal
// instead of a top-level page section, so the day-to-day scroll path is
// shorter for the common case (operators rarely audit individual JTIs).
//
// Data source: the existing AgentGrantResolution.live_tokens array that
// AgentDetail already fetches from /agents/:id. We do NOT introduce a new
// endpoint here; if a session field is missing in the analytics payload we
// render an em-dash. Revoke is wired through a caller-supplied prop so the
// modal stays presentational — when no revoke endpoint exists the parent
// passes undefined and we render a disabled button with a hint tooltip.

import { useEffect } from "preact/hooks";
import type { AgentGrantResolution } from "../../api/analytics";
import { fmtAge } from "../../util/time";

interface Props {
  open: boolean;
  onClose: () => void;
  agentLabel: string;
  grant?: AgentGrantResolution;
  /**
   * Optional revoke handler. When provided, each row renders an enabled
   * "Revoke" button that invokes this with the token's JTI. When omitted
   * the button is disabled with a "coming soon" tooltip — the policy
   * spec leaves the revoke surface for a later task.
   */
  onRevoke?: (jti: string) => Promise<void> | void;
}

export function SessionsModal({
  open,
  onClose,
  agentLabel,
  grant,
  onRevoke,
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
  const tokens = grant?.live_tokens || [];

  return (
    <div
      class="modal-backdrop"
      onClick={(e) => {
        if (e.target === e.currentTarget) onClose();
      }}
    >
      <div
        class="modal-card sessions-modal"
        role="dialog"
        aria-modal="true"
        aria-label={`active sessions for ${agentLabel}`}
      >
        <div class="modal-header">
          <div class="modal-title">active sessions</div>
          <div class="modal-sub">
            {agentLabel} · {tokens.length} live token
            {tokens.length === 1 ? "" : "s"}
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
          {tokens.length === 0 ? (
            <div class="empty-state">
              no live tokens for this agent — either the agent hasn't completed
              oauth yet, or all issued tokens have expired.
            </div>
          ) : (
            <table class="sessions-table">
              <thead>
                <tr>
                  <th>jti</th>
                  <th>dpop jkt</th>
                  <th>auth_time</th>
                  <th>acr</th>
                  <th class="right">grants</th>
                  <th class="right">actions</th>
                </tr>
              </thead>
              <tbody>
                {tokens.map((tok) => (
                  <SessionRow
                    key={tok.jti}
                    jti={tok.jti}
                    jkt={tok.jkt}
                    authTime={tok.auth_time}
                    acr={tok.acr}
                    grantCount={tok.grant_count}
                    onRevoke={onRevoke}
                  />
                ))}
              </tbody>
            </table>
          )}
          <div class="sessions-modal-footer">
            <span class="hint-text">
              prism doesn't keep refresh tokens beyond their lifetime — to
              force re-auth on every session, remove the agent under Manage
              below.
            </span>
          </div>
        </div>
      </div>
    </div>
  );
}

interface RowProps {
  jti: string;
  jkt?: string;
  authTime?: string;
  acr?: string;
  grantCount: number;
  onRevoke?: (jti: string) => Promise<void> | void;
}

function SessionRow({ jti, jkt, authTime, acr, grantCount, onRevoke }: RowProps) {
  const revokeDisabled = !onRevoke;
  return (
    <tr>
      <td>
        <code class="session-jti" title={jti}>
          {short(jti)}
        </code>
      </td>
      <td>
        {jkt ? (
          <span class="session-dpop-on" title={`DPoP-bound (jkt ${jkt})`}>
            <code>{short(jkt)}</code>
          </span>
        ) : (
          <span class="session-dpop-off">none</span>
        )}
      </td>
      <td class="session-meta">{authTime ? fmtAge(authTime) : "—"}</td>
      <td class="session-meta">{acr || "—"}</td>
      <td class="right session-meta">{grantCount}</td>
      <td class="right">
        <button
          type="button"
          class="session-revoke-btn"
          disabled={revokeDisabled}
          title={
            revokeDisabled
              ? "Revoke endpoint coming soon"
              : "revoke this token now"
          }
          onClick={() => {
            if (!onRevoke) return;
            void onRevoke(jti);
          }}
        >
          revoke
        </button>
      </td>
    </tr>
  );
}

function short(value: string): string {
  if (!value) return "";
  if (value.length <= 14) return value;
  return `${value.slice(0, 8)}…${value.slice(-4)}`;
}
