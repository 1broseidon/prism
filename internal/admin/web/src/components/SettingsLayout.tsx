import type { ComponentChildren } from "preact";
import { useLocation } from "preact-iso";

interface Tab {
  href: string;
  label: string;
  matchPrefix?: string;
}

const TABS: Tab[] = [
  { href: "/settings/network", label: "network" },
  { href: "/settings/storage", label: "storage", matchPrefix: "/settings/storage" },
  { href: "/settings/sign-in", label: "sign-in" },
];

export function SettingsLayout({ children }: { children: ComponentChildren }) {
  const loc = useLocation();
  const path = loc.path || "/";

  return (
    <div class="settings-shell">
      <nav class="settings-tabs" aria-label="Settings sections">
        {TABS.map((t) => {
          const prefix = t.matchPrefix ?? t.href;
          const active = path === t.href || path.startsWith(prefix + "/");
          return (
            <a
              key={t.href}
              href={t.href}
              class={active ? "settings-tab settings-tab-active" : "settings-tab"}
            >
              {t.label}
            </a>
          );
        })}
      </nav>
      <div class="settings-body">{children}</div>
    </div>
  );
}
