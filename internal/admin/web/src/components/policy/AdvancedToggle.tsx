// AdvancedToggle — small chip-style switch operators flip to reveal the
// raw template/binding/scope plumbing. State and event are owned by
// useAdvanced; this component is purely presentational.
//
// Renamed from PowerToolsToggle in the policy-refine rework (task-38). The
// user-facing affordance is now labeled "Advanced" — the underlying
// concept (raw primitives surfaced for power users) is unchanged.
import { useAdvanced, toggleAdvanced } from "../../hooks/useAdvanced";

interface Props {
  // Optional class hook so the toggle can be tucked into different chrome
  // surfaces (page header, sidebar footer) without forking the markup.
  class?: string;
}

export function AdvancedToggle({ class: cls = "" }: Props) {
  const on = useAdvanced();
  const label = on ? "Advanced: on" : "Advanced: off";
  return (
    <button
      type="button"
      role="switch"
      aria-checked={on}
      aria-label="Toggle Advanced mode"
      title={label}
      class={`advanced-toggle ${on ? "is-on" : "is-off"} ${cls}`.trim()}
      onClick={toggleAdvanced}
    >
      <span class="advanced-toggle-track" aria-hidden="true">
        <span class="advanced-toggle-knob" />
      </span>
      <span class="advanced-toggle-label">Advanced</span>
    </button>
  );
}
