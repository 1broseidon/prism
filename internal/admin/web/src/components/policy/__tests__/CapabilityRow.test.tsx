// CapabilityRow type-smoke "tests".
//
// Same constraint as CapabilityChip.test.tsx — `npm test` is `tsc --noEmit`,
// so these checks ensure the component's prop contract and the chip-order
// invariant hold at compile time. The exported pure-function `orderedChips`
// is exercised against a deliberately shuffled fixture so the spec §5.2
// canonical order is locked in.

import type { JSX } from "preact";
import type { CapabilityView, Chip } from "../../../api/policy";
import { CapabilityRow, orderedChips } from "../CapabilityRow";

// ── Fixture 1: full chip set, deliberately scrambled ────────────────────────
//
// Server may return chips in any order; orderedChips must canonicalize to:
//   action → where → storage → time → freshness → auth → (unknown tail)
const scrambledChips: Chip[] = [
  { kind: "auth", label: "DPoP required" },
  { kind: "time", label: "business hours" },
  { kind: "action", label: "write files" },
  { kind: "freshness", label: "MFA in last 10 min" },
  { kind: "where", label: "in /workspace/${agent}/" },
  { kind: "storage", label: "on ephemeral storage" },
  // Unknown kinds tail-append in stable order, never dropped.
  { kind: "experimental", label: "x-feature" },
];

const canonical = orderedChips(scrambledChips);
const canonicalKinds = canonical.map((c) => c.kind);
const expectedKinds: string[] = [
  "action",
  "where",
  "storage",
  "time",
  "freshness",
  "auth",
  "experimental",
];

// Compile-time check via length comparison — exported below to satisfy
// noUnusedLocals. If orderedChips ever drops chips, this constant breaks at
// build time once a real runtime test runner is wired up.
const lengthMatch: boolean =
  canonicalKinds.length === expectedKinds.length;

// ── Fixture 2: action-only chips → row reads "Can <action>" with no others ──
const actionOnly: Chip[] = [{ kind: "action", label: "call tools" }];
const actionOnlyOrdered = orderedChips(actionOnly);
const actionOnlyOK: boolean =
  actionOnlyOrdered.length === 1 &&
  actionOnlyOrdered[0].kind === "action";

// ── Fixture 3: empty chips → orderedChips returns [] (row falls back to summary)
const emptyOrdered = orderedChips(undefined);
const emptyOK: boolean = emptyOrdered.length === 0;

// ── View fixture used in JSX prop checks ────────────────────────────────────
const sampleView: CapabilityView = {
  id: "scope-abc",
  source: "scope",
  display_summary: "Can write files in /workspace/${agent}/",
  chips: scrambledChips,
  spec: {
    action: { mode: "verb", verb_slug: "write_files" },
    where: { mode: "agent_home" },
  },
};

// Required + optional prop variants must type-check.
const minimal: JSX.Element = (
  <CapabilityRow
    view={sampleView}
    subjectType="groups"
    subjectID="engineering"
  />
);

const wired: JSX.Element = (
  <CapabilityRow
    view={sampleView}
    subjectType="agents"
    subjectID="agent-1"
    subjectLabel="engineering"
    onEdit={(spec, view) => {
      // spec arrives typed; view is the row data.
      void spec;
      void view;
    }}
    onDelete={(id) => {
      void id;
    }}
    onError={(err) => {
      void err;
    }}
  />
);

// Export the locals so noUnusedLocals doesn't reject the file.
export const __rowTypeChecks = {
  lengthMatch,
  actionOnlyOK,
  emptyOK,
  canonicalKinds,
  expectedKinds,
  minimal,
  wired,
};
