import type { ComponentChildren } from "preact";

export function Field({
  label,
  children,
}: {
  label: string;
  children: ComponentChildren;
}) {
  return (
    <div class="config-field">
      <label class="config-label">{label}</label>
      {children}
    </div>
  );
}
