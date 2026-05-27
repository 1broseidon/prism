// AddCapabilityModal type-smoke "tests".
//
// `npm test` in this package is `tsc --noEmit`, so this file pins the modal's
// prop contract + a few invariants that must hold for the round-trip flow:
//   1. Required props (mode, subjectType, subjectID, onCancel, onSaved) are
//      enforced — both add and edit prop variants type-check.
//   2. The `hasRawFallback` helper distinguishes a normal advanced spec from
//      one carrying the internal `_tool` predicate (the verb compile path).
//   3. `specMatchesAgentHome` recognizes the canonical "${agent.prism_id}"
//      template prefix used by the "Only their own workspace" preset.
//
// When a real test runner is wired up these can be promoted to behavioral
// checks (mount the modal, simulate clicks, assert payloads on save).

import type { JSX } from "preact";
import type {
  CapabilitySpec,
  CapabilityView,
} from "../../../api/policy";
import {
  AddCapabilityModal,
  hasRawFallback,
  specMatchesAgentHome,
} from "../AddCapabilityModal";

// ── Fixture: a fully-constrained edit-mode capability ──────────────────────
const editSpec: CapabilitySpec = {
  action: { mode: "verb", verb_slug: "write_files" },
  where: { mode: "agent_home" },
  when: { mode: "business", timezone: "America/Toronto" },
  how_secure: { mode: "mfa", mfa_freshness_sec: 600 },
};

const editView: CapabilityView = {
  id: "grant-binding-1",
  source: "grant",
  display_summary:
    "Can write files in own workspace during business hours with MFA",
  chips: [
    { kind: "action", label: "write files" },
    { kind: "where", label: "own workspace" },
    { kind: "time", label: "business hours" },
    { kind: "freshness", label: "MFA in last 10 min" },
  ],
  spec: editSpec,
};

// ── Add-mode minimal prop set ──────────────────────────────────────────────
const addMinimal: JSX.Element = (
  <AddCapabilityModal
    mode="add"
    subjectType="groups"
    subjectID="engineering"
    onCancel={() => {
      /* no-op */
    }}
    onSaved={(view) => {
      // view is CapabilityView — locked at compile time.
      void view;
    }}
  />
);

// ── Edit-mode full prop set ─────────────────────────────────────────────────
const editFull: JSX.Element = (
  <AddCapabilityModal
    mode="edit"
    subjectType="groups"
    subjectID="engineering"
    subjectLabel="Engineering"
    initialSpec={editSpec}
    initialView={editView}
    availableRoles={["admin", "operator"] as const}
    onCancel={() => {
      /* no-op */
    }}
    onSaved={(view) => {
      void view;
    }}
  />
);

// ── Reverse-mapping helper checks ──────────────────────────────────────────

// `_tool` is the server-internal verb compile predicate — its presence MUST
// trip the raw fallback so we don't pretend to round-trip a synthesised arg.
const specWithToolInternal: CapabilitySpec = {
  action: { mode: "verb", verb_slug: "write_files" },
  advanced: {
    args: {
      _tool: { tool_in_set: ["fs:write_file", "fs:append_file"] },
    },
  },
};
const rawFallbackTrips: boolean = hasRawFallback(specWithToolInternal);

// A plain advanced spec (no internal predicates) should NOT trip the
// fallback — the structured editor can render it just fine.
const specWithPlainAdvanced: CapabilitySpec = {
  action: { mode: "verb", verb_slug: "write_files" },
  advanced: {
    role_required: "operator",
  },
};
const rawFallbackOK: boolean = !hasRawFallback(specWithPlainAdvanced);

// Empty / missing advanced never trips the fallback.
const rawFallbackEmpty: boolean = !hasRawFallback(undefined);

// Agent-home recognition — the canonical template string must round-trip.
const agentHomeMatches: boolean = specMatchesAgentHome(
  "/workspace/${agent.prism_id}/",
);
const agentHomeRejectsOther: boolean = !specMatchesAgentHome(
  "/workspace/something-else/",
);

// Export the locals so noUnusedLocals doesn't reject the file.
export const __addCapModalTypeChecks = {
  addMinimal,
  editFull,
  rawFallbackTrips,
  rawFallbackOK,
  rawFallbackEmpty,
  agentHomeMatches,
  agentHomeRejectsOther,
};
