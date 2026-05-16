import { useEffect, useMemo, useState, useRef } from "preact/hooks";
import { useLocation } from "preact-iso";
import { backends, agents, groups } from "../state";

interface Item {
  id: string;
  kind: "page" | "server" | "agent" | "group" | "tool";
  title: string;
  subtitle: string;
  href: string;
}

const PAGE_ITEMS: Item[] = [
  {
    id: "page-overview",
    kind: "page",
    title: "overview",
    subtitle: "system status and recent activity",
    href: "/",
  },
  {
    id: "page-servers",
    kind: "page",
    title: "mcp servers",
    subtitle: "backends and tools",
    href: "/servers",
  },
  {
    id: "page-identity",
    kind: "page",
    title: "identity",
    subtitle: "agents, groups, default scopes",
    href: "/identity",
  },
  {
    id: "page-audit",
    kind: "page",
    title: "audit",
    subtitle: "events and filters",
    href: "/audit",
  },
];

function fuzzyScore(query: string, target: string): number {
  if (!query) return 1;
  const q = query.toLowerCase();
  const t = target.toLowerCase();
  if (t === q) return 100;
  if (t.startsWith(q)) return 80;
  if (t.includes(q)) return 50;
  // very loose: every char in query appears in order
  let qi = 0;
  for (let i = 0; i < t.length && qi < q.length; i++) {
    if (t[i] === q[qi]) qi++;
  }
  return qi === q.length ? 20 : 0;
}

export function CommandPalette() {
  const [open, setOpen] = useState(false);
  const [query, setQuery] = useState("");
  const [selected, setSelected] = useState(0);
  const loc = useLocation();
  const inputRef = useRef<HTMLInputElement>(null);

  useEffect(() => {
    const onKey = (e: KeyboardEvent) => {
      if ((e.metaKey || e.ctrlKey) && e.key === "k") {
        e.preventDefault();
        setOpen((v) => !v);
        setQuery("");
        setSelected(0);
      } else if (e.key === "Escape" && open) {
        e.preventDefault();
        setOpen(false);
      }
    };
    window.addEventListener("keydown", onKey);
    return () => window.removeEventListener("keydown", onKey);
  }, [open]);

  useEffect(() => {
    if (open) {
      // Focus on next tick
      setTimeout(() => inputRef.current?.focus(), 10);
    }
  }, [open]);

  const items = useMemo<Item[]>(() => {
    const result: Item[] = [...PAGE_ITEMS];
    for (const b of backends.data.value || []) {
      result.push({
        id: `server-${b.id}`,
        kind: "server",
        title: b.id,
        subtitle: `mcp server · ${b.url || "stdio"}`,
        href: `/servers/${encodeURIComponent(b.id)}`,
      });
      for (const t of b.tools || []) {
        result.push({
          id: `tool-${t.name}`,
          kind: "tool",
          title: t.name,
          subtitle: t.description
            ? t.description.slice(0, 80) + (t.description.length > 80 ? "…" : "")
            : `tool on ${b.id}`,
          href: `/servers/${encodeURIComponent(b.id)}`,
        });
      }
    }
    for (const a of agents.data.value || []) {
      if (!a.prism_id) continue;
      const display = a.label || a.description || a.client_id;
      result.push({
        id: `agent-${a.client_id}`,
        kind: "agent",
        title: display,
        subtitle: `${a.dynamic ? "dynamic" : "static"} agent · ${a.client_id}`,
        href: `/identity/agents/${encodeURIComponent(a.prism_id)}`,
      });
    }
    for (const g of groups.data.value || []) {
      result.push({
        id: `group-${g.name}`,
        kind: "group",
        title: g.name,
        subtitle: `${g.source} group · ${g.scopes.length} scope${g.scopes.length === 1 ? "" : "s"}`,
        href: `/identity/groups/${encodeURIComponent(g.name)}`,
      });
    }
    return result;
  }, [backends.data.value, agents.data.value, groups.data.value]);

  const filtered = useMemo(() => {
    if (!query.trim()) return items.slice(0, 30);
    const q = query.trim();
    const scored = items
      .map((item) => ({
        item,
        score: Math.max(fuzzyScore(q, item.title), fuzzyScore(q, item.subtitle) * 0.6),
      }))
      .filter((s) => s.score > 0)
      .sort((a, b) => b.score - a.score)
      .slice(0, 30)
      .map((s) => s.item);
    return scored;
  }, [items, query]);

  useEffect(() => {
    if (selected >= filtered.length) setSelected(0);
  }, [filtered, selected]);

  if (!open) return null;

  const choose = (item: Item) => {
    loc.route(item.href);
    setOpen(false);
  };

  const onKey = (e: KeyboardEvent) => {
    if (e.key === "ArrowDown") {
      e.preventDefault();
      setSelected((i) => Math.min(i + 1, filtered.length - 1));
    } else if (e.key === "ArrowUp") {
      e.preventDefault();
      setSelected((i) => Math.max(i - 1, 0));
    } else if (e.key === "Enter") {
      e.preventDefault();
      const item = filtered[selected];
      if (item) choose(item);
    }
  };

  return (
    <div class="palette-overlay" onClick={() => setOpen(false)}>
      <div class="palette" onClick={(e) => e.stopPropagation()}>
        <input
          ref={inputRef}
          type="text"
          placeholder="jump to anywhere…"
          class="palette-input"
          value={query}
          onInput={(e) => setQuery((e.target as HTMLInputElement).value)}
          onKeyDown={onKey}
          spellcheck={false}
          autocomplete="off"
        />
        <div class="palette-list">
          {filtered.length === 0 ? (
            <div class="palette-empty">no matches.</div>
          ) : (
            filtered.map((item, idx) => (
              <button
                key={item.id}
                class={
                  idx === selected
                    ? "palette-item palette-item-selected"
                    : "palette-item"
                }
                onClick={() => choose(item)}
                onMouseEnter={() => setSelected(idx)}
              >
                <span class={`palette-kind palette-kind-${item.kind}`}>
                  {item.kind}
                </span>
                <span class="palette-title">{item.title}</span>
                <span class="palette-subtitle">{item.subtitle}</span>
              </button>
            ))
          )}
        </div>
        <div class="palette-footer">
          <span>
            <kbd>↑</kbd> <kbd>↓</kbd> navigate
          </span>
          <span>
            <kbd>↵</kbd> select
          </span>
          <span>
            <kbd>esc</kbd> close
          </span>
        </div>
      </div>
    </div>
  );
}
