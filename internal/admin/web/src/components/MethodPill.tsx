// Small visual pill for HTTP methods (GET/POST/etc). Extracted from the
// OpenAPI picker so unrelated surfaces (re-import diff, server detail) can
// reuse it without pulling in the picker module.

export function MethodPill({ method }: { method: string }) {
  const m = (method || "").toUpperCase();
  const cls = m
    ? `method-pill method-pill-${m.toLowerCase()}`
    : "method-pill method-pill-unknown";
  return <span class={cls}>{m || "?"}</span>;
}
