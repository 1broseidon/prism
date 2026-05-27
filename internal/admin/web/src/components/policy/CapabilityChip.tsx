// CapabilityChip — pill primitive used by CapabilityRow.
//
// One component per spec §5.2: the action chip is rendered with a heavier
// visual weight via kind="action"; constraint chips share the lighter
// styling. Class names lower-case the kind so the CSS can target each one
// (.policy-chip-action, .policy-chip-time, …).
//
// The optional `tooltip` (or fallback `value`) shows the underlying raw
// value on hover — useful when the label is truncated (full path prefix,
// full time window, etc.).

import type { JSX } from "preact";

export interface CapabilityChipProps {
  /** Discriminator: "action" | "where" | "storage" | "time" | "freshness" | "auth". */
  kind: string;
  /** Short user-visible label, e.g. "write files" or "in /workspace/…". */
  label: string;
  /** Underlying raw value the chip stands in for (full path prefix, etc.). */
  value?: string;
  /** Explicit tooltip override; defaults to `value` when present. */
  tooltip?: string;
  /** Optional click handler — surfaces edit-on-chip in later tasks. */
  onClick?: JSX.MouseEventHandler<HTMLSpanElement>;
}

export function CapabilityChip({
  kind,
  label,
  value,
  tooltip,
  onClick,
}: CapabilityChipProps) {
  const title = tooltip ?? value ?? label;
  const safeKind = (kind || "constraint").toLowerCase();
  const cls =
    safeKind === "action"
      ? "policy-chip policy-chip-action"
      : `policy-chip policy-chip-constraint policy-chip-${safeKind}`;
  return (
    <span
      class={cls}
      title={title}
      data-kind={safeKind}
      onClick={onClick}
      role={onClick ? "button" : undefined}
      tabIndex={onClick ? 0 : undefined}
    >
      {label}
    </span>
  );
}
