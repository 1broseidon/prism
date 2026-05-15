interface Props {
  scopes: string[];
  empty?: string;
  denied?: boolean;
}

export function ScopeList({ scopes, empty, denied }: Props) {
  if (scopes.length === 0) {
    if (empty) {
      return (
        <span style="color:var(--muted);font-size:10px;font-style:italic">
          {empty}
        </span>
      );
    }
    return null;
  }
  return (
    <div class="scope-list">
      {scopes.map((s) => (
        <span class={denied ? "scope-tag denied" : "scope-tag"} key={s}>
          {s}
        </span>
      ))}
    </div>
  );
}
