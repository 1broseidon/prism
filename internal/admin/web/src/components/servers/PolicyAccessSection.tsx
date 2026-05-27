// PolicyAccessSection — "Who can use this?" on /servers/{id}.
//
// Task-43: brings spec §10.2's reverse-policy view forward into v1. Closes
// the policy-everywhere loop on the backend detail page — operators clicking
// a server can now see which subjects have access, via what capability shape,
// and how often they actually call (allow + denials over 24h).
//
// Behavior contract (matches PolicyHealthStrip's pattern, with a longer
// refresh interval because the aggregates change less):
//
//   - Single endpoint fetch: GET /api/v1/policy/access?backend={id}.
//   - 60s background refresh (Health strip uses 30s — these aggregates
//     change less often, so we spend half the request budget here).
//   - Errors render an inline retry chip but never block the rest of the
//     ServerDetail page (ToolsSection / ActivitySection keep rendering).
//   - Empty state — no policy grants access — shows the explicit copy and
//     a deep-link to /policy so the operator can fix it in one click.
//
// Re-uses existing `section` / `card` / `empty-state` classes — no new CSS
// tokens. Subject type chips reuse pill styles already in the design system.

import {
  getPolicyAccess,
  type PolicyAccessEntry,
  type PolicyAccessResponse,
} from "../../api/policy";
import { usePolledFetch } from "../../hooks/usePolledFetch";

const REFRESH_INTERVAL_MS = 60_000;

interface PolicyAccessSectionProps {
  backendId: string;
}

export function PolicyAccessSection({ backendId }: PolicyAccessSectionProps) {
  // Initial fetch + 60s interval; cancelled on unmount and on backend id
  // change (the deps array triggers a fresh fetch when backendId changes).
  const { data, error, loading, retry } = usePolledFetch<PolicyAccessResponse>(
    () => getPolicyAccess(backendId),
    REFRESH_INTERVAL_MS,
    [backendId],
  );

  return (
    <div class="section">
      <div class="section-header">
        <span class="section-title">who can use this?</span>
        <span class="section-sub">
          subjects whose policy grants access, with 24h call + deny counts.
        </span>
      </div>
      <PolicyAccessBody
        loading={loading}
        error={error}
        data={data}
        onRetry={retry}
      />
    </div>
  );
}

interface BodyProps {
  loading: boolean;
  error: string | null;
  data: PolicyAccessResponse | null;
  onRetry: () => void;
}

function PolicyAccessBody({ loading, error, data, onRetry }: BodyProps) {
  // First-paint placeholder. Renders an empty card to avoid layout shift
  // when the response lands — same tactic the Health strip uses.
  if (loading && !data) {
    return (
      <div class="card policy-access-loading" aria-busy="true">
        loading…
      </div>
    );
  }

  // Inline error — the rest of ServerDetail keeps rendering. Operators
  // can retry without leaving the page.
  if (error && !data) {
    return (
      <div class="card policy-access-error" role="alert">
        <span>policy access unavailable: {error}</span>
        <button type="button" class="section-btn" onClick={onRetry}>
          retry
        </button>
      </div>
    );
  }

  if (!data) return null;

  if (data.empty) {
    // Explicit "No policy grants access" copy with a one-click route to
    // Policy Builder — the operator needs an obvious next step rather
    // than guessing where to add a grant.
    return (
      <div class="card empty-state policy-access-empty">
        <div>No policy grants access to this backend.</div>
        <div class="hint-text">
          Use{" "}
          <a href="/policy" class="link-accent">
            Policy Builder
          </a>{" "}
          to grant a group, role, or agent the ability to call this server.
        </div>
      </div>
    );
  }

  // Normal render — table of subjects, grouped by type. We surface the
  // subject type prominently because operators reason about access in
  // those buckets ("which agents are calling?", "which groups have rights?").
  return (
    <div class="card">
      <table class="policy-access-table">
        <thead>
          <tr>
            <th>subject</th>
            <th>via</th>
            <th>capability</th>
            <th class="right">calls 24h</th>
            <th class="right">denials 24h</th>
            <th />
          </tr>
        </thead>
        <tbody>
          {data.entries.map((entry) => (
            <PolicyAccessRow
              key={`${entry.subject_type}|${entry.subject_id}|${entry.capability_id}`}
              entry={entry}
            />
          ))}
        </tbody>
      </table>
    </div>
  );
}

function PolicyAccessRow({ entry }: { entry: PolicyAccessEntry }) {
  const editHref = `/policy/${encodeURIComponent(entry.subject_type)}/${encodeURIComponent(entry.subject_id)}`;
  return (
    <tr>
      <td>
        <span class={`pill pill-neutral pill-${entry.subject_type}`}>
          {entry.subject_type}
        </span>{" "}
        <span class="meta-value-mono">{entry.subject_id}</span>
      </td>
      <td>
        <SourcePill source={entry.source} />
        {entry.template_hash && (
          <span
            class="template-hash"
            title={`Template hash: ${entry.template_hash}`}
          >
            {" "}
            {shortHash(entry.template_hash)}
          </span>
        )}
      </td>
      <td class="policy-access-summary">{entry.summary}</td>
      <td class="right">{entry.calls_24h}</td>
      <td class="right">
        {entry.denials_24h > 0 ? (
          <span class="denied-text">{entry.denials_24h}</span>
        ) : (
          entry.denials_24h
        )}
      </td>
      <td class="right">
        <a class="link-accent" href={editHref}>
          edit policy →
        </a>
      </td>
    </tr>
  );
}

function SourcePill({ source }: { source: PolicyAccessEntry["source"] }) {
  // "scope" rows are a coarse grant (a "backend:tool" or "backend:*" string
  // on the subject's policy). "grant" rows are template-bound and carry a
  // template hash; we tag them so operators can correlate with Power Tools.
  if (source === "grant") {
    return <span class="pill pill-neutral">grant</span>;
  }
  return <span class="pill pill-neutral">scope</span>;
}

// shortHash trims the canonical "sha256-…" template hash to the first 10
// characters of the digest so it fits inline. Hovering the title reveals
// the full hash for copy/paste into Power Tools.
function shortHash(hash: string): string {
  const stripped = hash.startsWith("sha256-") ? hash.slice("sha256-".length) : hash;
  return stripped.length > 10 ? stripped.slice(0, 10) + "…" : stripped;
}
