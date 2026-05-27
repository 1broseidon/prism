// TabStrip — small reusable tab strip used by surfaces with a flat, page-
// level segmented nav (Agents tabs: Members | Groups | Roles, and similar).
//
// Why a new primitive instead of reusing SettingsLayout? SettingsLayout owns
// its own tab list and a shell wrapper; AdvancedSubnav uses bespoke
// `.grant-tab` classes. Neither was reusable from /agents without dragging
// extra structure along. TabStrip is a 30-line, prop-driven primitive that
// renders the existing `.tabs` / `.tab` CSS classes (already defined in
// styles/app.css since the early dashboard work) so the visual language
// stays consistent.
//
// Each tab is an `<a href>` so the browser handles middle-click/cmd-click
// like every other nav surface. Active matching is exact by default;
// callers pass `matchPrefix` for nested routes that should still light up
// the parent tab.

import { useLocation } from "preact-iso";

export interface TabItem {
  href: string;
  label: string;
  // matchPrefix lights up the tab when the current path equals href OR
  // starts with `${matchPrefix}/`. Defaults to href.
  matchPrefix?: string;
}

interface Props {
  tabs: TabItem[];
  // Optional aria-label override; defaults to "Tabs" for accessibility.
  ariaLabel?: string;
}

export function TabStrip({ tabs, ariaLabel = "Tabs" }: Props) {
  const loc = useLocation();
  const path = loc.path || "/";
  return (
    <nav class="tabs" aria-label={ariaLabel}>
      {tabs.map((t) => {
        const prefix = t.matchPrefix ?? t.href;
        const active = path === t.href || path.startsWith(prefix + "/");
        return (
          <a
            key={t.href}
            href={t.href}
            class={active ? "tab active" : "tab"}
            aria-current={active ? "page" : undefined}
          >
            {t.label}
          </a>
        );
      })}
    </nav>
  );
}
