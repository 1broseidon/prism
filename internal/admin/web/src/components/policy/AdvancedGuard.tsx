// AdvancedGuard — silent gating wrapper for routes that operators only see
// when the Advanced toggle is ON (spec §11).
//
// Renamed from PowerToolsGuard in the policy-refine rework (task-38). The
// gate semantics are unchanged: the URL stays routable and we render an
// in-place "enable Advanced" panel when the toggle is OFF.
//
// When OFF:
//   - The URL is still routable (no 404).
//   - We render a small "Enable Advanced to access this page" panel with
//     a button that flips the toggle in place. Once it flips, the wrapped
//     content renders without a navigation round-trip.
//
// When ON:
//   - We render the wrapped component as-is. No additional chrome.
//
// Advanced state is read via the canonical useAdvanced() hook — never
// duplicate the localStorage read (spec §11 + the hook contract).

import type { ComponentChildren } from "preact";
import { toggleAdvanced, useAdvanced } from "../../hooks/useAdvanced";

interface Props {
  children: ComponentChildren;
}

export function AdvancedGuard({ children }: Props) {
  const on = useAdvanced();
  if (on) return <>{children}</>;
  return <AdvancedLocked />;
}

function AdvancedLocked() {
  return (
    <div
      class="advanced-locked"
      role="note"
      aria-label="Advanced required"
    >
      <div class="advanced-locked-card">
        <div class="advanced-locked-title">Advanced required</div>
        <div class="advanced-locked-body">
          This page exposes raw scope strings, grant templates, and binding
          plumbing. It's hidden by default to keep the policy surface focused
          on capabilities.
        </div>
        <button
          type="button"
          class="advanced-locked-btn"
          onClick={() => toggleAdvanced()}
        >
          Enable Advanced
        </button>
      </div>
    </div>
  );
}
