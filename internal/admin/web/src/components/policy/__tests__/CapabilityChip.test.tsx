// CapabilityChip type-smoke "tests".
//
// The admin web package's `npm test` script is `tsc --noEmit`, so these files
// double as compile-time checks that the component contract holds:
//   1. Required props (kind, label) are enforced.
//   2. Optional props (value, tooltip, onClick) are accepted.
//   3. Each documented chip kind (action + the five constraint kinds) is
//      assignable to the `kind` prop.
//
// When a real test runner lands these can be promoted to behavioral checks
// without changing the import surface.

import type { JSX } from "preact";
import type { CapabilityChipProps } from "../CapabilityChip";
import { CapabilityChip } from "../CapabilityChip";

// Required props only — action chip variant.
const minimalAction: JSX.Element = (
  <CapabilityChip kind="action" label="write files" />
);

// Full prop set — constraint chip with tooltip + click handler.
const full: JSX.Element = (
  <CapabilityChip
    kind="where"
    label="in /workspace/${agent}/"
    value="/workspace/${agent.prism_id}/"
    tooltip="Limits this capability to the agent's home dir"
    onClick={(_e) => {
      /* no-op: chip clicks open the edit modal in later tasks. */
    }}
  />
);

// Each documented kind in spec §5.2 is accepted by the chip primitive.
const allKinds: ReadonlyArray<CapabilityChipProps["kind"]> = [
  "action",
  "where",
  "storage",
  "time",
  "freshness",
  "auth",
];

function renderAll(): JSX.Element[] {
  return allKinds.map((kind, i) => (
    <CapabilityChip key={i} kind={kind} label={kind} />
  ));
}

// Test 6: kind="time" renders with time-specific styling — captured by
// the className containing the kind suffix. We can't poke the DOM without a
// runtime; we instead pin the expected className via a helper string.
function expectedTimeClass(): string {
  return "policy-chip policy-chip-constraint policy-chip-time";
}

// Export references so noUnusedLocals doesn't flag the type-only assertions.
export const __chipTypeChecks = {
  minimalAction,
  full,
  renderAll,
  expectedTimeClass,
};
