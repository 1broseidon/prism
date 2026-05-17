import type { ComponentChildren } from "preact";
import { useEffect, useState } from "preact/hooks";
import { useLocation } from "preact-iso";
import { info } from "../state";
import { me, refreshMe } from "../state/me";
import { fmtUptime } from "../util/time";

interface Props {
  children: ComponentChildren;
}

interface NavItem {
  href: string;
  label: string;
}

const CONSOLE_NAV: NavItem[] = [
  { href: "/", label: "Overview" },
  { href: "/servers", label: "Servers" },
  { href: "/agents", label: "Agents" },
  { href: "/policy", label: "Policy" },
  { href: "/activity", label: "Activity" },
];

export function Layout({ children }: Props) {
  const loc = useLocation();
  const path = loc.path || "/";
  const i = info.data.value;
  const m = me.value;
  const [navOpen, setNavOpen] = useState(false);

  // Close the mobile drawer whenever the route changes — feels expected and
  // avoids a stale-open drawer on first paint of the new page.
  useEffect(() => {
    setNavOpen(false);
  }, [path]);

  // Lock body scroll while the drawer is open so the page underneath doesn't
  // rubber-band on iOS. The shell-content scroll container handles its own.
  useEffect(() => {
    if (navOpen) {
      document.body.dataset.navOpen = "true";
      const onKey = (e: KeyboardEvent) => {
        if (e.key === "Escape") setNavOpen(false);
      };
      window.addEventListener("keydown", onKey);
      return () => {
        delete document.body.dataset.navOpen;
        window.removeEventListener("keydown", onKey);
      };
    }
    delete document.body.dataset.navOpen;
    return undefined;
  }, [navOpen]);

  const signOut = async () => {
    try {
      await fetch("/api/v1/auth/logout", { method: "POST" });
    } catch {
      // ignore; we'll still refresh me below
    }
    await refreshMe();
    loc.route("/");
    setNavOpen(false);
  };

  const inSettings = path.startsWith("/settings");

  const renderNavLink = (n: NavItem) => {
    const active =
      path === n.href || (n.href !== "/" && path.startsWith(n.href));
    return (
      <a
        key={n.href}
        href={n.href}
        class={active ? "nav-link active" : "nav-link"}
      >
        {n.label}
      </a>
    );
  };

  return (
    <div class={navOpen ? "shell shell-nav-open" : "shell"}>
      <div class="shell-logo">
        <button
          class="nav-toggle"
          type="button"
          aria-label={navOpen ? "Close navigation" : "Open navigation"}
          aria-expanded={navOpen}
          aria-controls="primary-nav"
          onClick={() => setNavOpen((v) => !v)}
        >
          <span class="nav-toggle-bar" />
          <span class="nav-toggle-bar" />
          <span class="nav-toggle-bar" />
        </button>
        <a class="shell-brand" href="/">
          <div class="logo-mark">P</div>
          <h1>prism</h1>
        </a>
      </div>

      <header class="shell-header">
        <div class="shell-meta">
          {m?.auth === "session" && m.email && (
            <span class="shell-identity">
              <span class="shell-identity-email">{m.email}</span>
              {m.role === "viewer" && (
                <span class="shell-identity-role">viewer</span>
              )}
              <button class="shell-signout" onClick={signOut}>
                sign out
              </button>
            </span>
          )}
          <a
            class={inSettings ? "header-cog header-cog-active" : "header-cog"}
            href="/settings/network"
            aria-label="Settings"
            title="Settings"
          >
            <svg
              width="18"
              height="18"
              viewBox="0 0 24 24"
              fill="none"
              stroke="currentColor"
              stroke-width="1.75"
              stroke-linecap="round"
              stroke-linejoin="round"
              aria-hidden="true"
            >
              <circle cx="12" cy="12" r="3" />
              <path d="M19.4 15a1.65 1.65 0 0 0 .33 1.82l.06.06a2 2 0 0 1 0 2.83 2 2 0 0 1-2.83 0l-.06-.06a1.65 1.65 0 0 0-1.82-.33 1.65 1.65 0 0 0-1 1.51V21a2 2 0 0 1-2 2 2 2 0 0 1-2-2v-.09A1.65 1.65 0 0 0 9 19.4a1.65 1.65 0 0 0-1.82.33l-.06.06a2 2 0 0 1-2.83 0 2 2 0 0 1 0-2.83l.06-.06a1.65 1.65 0 0 0 .33-1.82 1.65 1.65 0 0 0-1.51-1H3a2 2 0 0 1-2-2 2 2 0 0 1 2-2h.09A1.65 1.65 0 0 0 4.6 9a1.65 1.65 0 0 0-.33-1.82l-.06-.06a2 2 0 0 1 0-2.83 2 2 0 0 1 2.83 0l.06.06a1.65 1.65 0 0 0 1.82.33H9a1.65 1.65 0 0 0 1-1.51V3a2 2 0 0 1 2-2 2 2 0 0 1 2 2v.09a1.65 1.65 0 0 0 1 1.51 1.65 1.65 0 0 0 1.82-.33l.06-.06a2 2 0 0 1 2.83 0 2 2 0 0 1 0 2.83l-.06.06a1.65 1.65 0 0 0-.33 1.82V9a1.65 1.65 0 0 0 1.51 1H21a2 2 0 0 1 2 2 2 2 0 0 1-2 2h-.09a1.65 1.65 0 0 0-1.51 1z" />
            </svg>
          </a>
        </div>
      </header>

      <button
        class="nav-scrim"
        aria-label="Close navigation"
        tabindex={-1}
        onClick={() => setNavOpen(false)}
      />

      <nav class="shell-nav" id="primary-nav">
        <div class="nav-section-label">Console</div>
        {CONSOLE_NAV.map(renderNavLink)}
        {i && (
          <div class="nav-meta">
            <span>v{i.version}</span>
            <span class="nav-meta-dot">·</span>
            <span>up {fmtUptime(i.uptime)}</span>
          </div>
        )}
      </nav>

      <main class="shell-content">{children}</main>
    </div>
  );
}
