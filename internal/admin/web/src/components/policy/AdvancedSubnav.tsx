// AdvancedSubnav — top-of-page subnav for the Advanced-gated grant pages
// (Cross-cutting / Templates / Bindings). Renamed from PowerToolsSubnav and
// now points exclusively at the consolidated /policy/advanced/* routes
// (task-38). The legacy /grants/* paths still resolve via 301 redirect so
// existing bookmarks survive.

import { useLocation } from "preact-iso";

interface Tab {
  href: string;
  label: string;
}

const TABS: Tab[] = [
  { href: "/policy/advanced", label: "Cross-cutting" },
  { href: "/policy/advanced/templates", label: "Templates" },
  { href: "/policy/advanced/bindings", label: "Bindings" },
];

export function AdvancedSubnav() {
  const loc = useLocation();
  const path = loc.path || "";
  return (
    <nav class="grant-tabs" aria-label="Advanced sections">
      {TABS.map((t) => {
        // Treat the bare /policy/advanced (Cross-cutting) tab as exact-match
        // only. Otherwise its prefix would also match /policy/advanced/templates
        // and /policy/advanced/bindings, lighting up two tabs at once.
        // Child tabs keep prefix-match so future nested subroutes still
        // highlight their parent.
        const active =
          path === t.href ||
          (t.href !== "/policy/advanced" && path.startsWith(`${t.href}/`));
        return (
          <a
            key={t.href}
            href={t.href}
            class={active ? "grant-tab active" : "grant-tab"}
            aria-current={active ? "page" : undefined}
          >
            {t.label}
          </a>
        );
      })}
    </nav>
  );
}
