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

const CONSOLE_FOOTER: NavItem[] = [{ href: "/settings/network", label: "Settings" }];

const SETTINGS_NAV: NavItem[] = [
  { href: "/settings/network", label: "Network" },
  { href: "/settings/storage", label: "Storage" },
  { href: "/settings/sign-in", label: "Sign-in" },
];

const SETTINGS_FOOTER: NavItem[] = [{ href: "/", label: "← Back to Console" }];

export function Layout({ children }: Props) {
  const loc = useLocation();
  const path = loc.path || "/";
  const i = info.data.value;
  const err = info.error.value;
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
      await fetch("/auth/logout", { method: "POST" });
    } catch {
      // ignore; we'll still refresh me below
    }
    await refreshMe();
    loc.route("/");
    setNavOpen(false);
  };

  const inSettings = path.startsWith("/settings");
  const navItems = inSettings ? SETTINGS_NAV : CONSOLE_NAV;
  const footerItems = inSettings ? SETTINGS_FOOTER : CONSOLE_FOOTER;
  const sectionLabel = inSettings ? "Settings" : "Console";

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
        <div class="logo-mark">P</div>
        <h1>prism</h1>
      </div>

      <header class="shell-header">
        <div class="status-badge">
          <div class={err ? "status-dot error" : "status-dot"} />
          <span class="status-text">{err ? "disconnected" : "live"}</span>
        </div>
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
          {i ? <span class="shell-meta-version">v{i.version}</span> : null}
          {i ? (
            <span class="shell-meta-uptime">up {fmtUptime(i.uptime)}</span>
          ) : null}
        </div>
      </header>

      <button
        class="nav-scrim"
        aria-label="Close navigation"
        tabindex={-1}
        onClick={() => setNavOpen(false)}
      />

      <nav class="shell-nav" id="primary-nav">
        <div class="nav-section-label">{sectionLabel}</div>
        {navItems.map(renderNavLink)}
        <div class="nav-footer">{footerItems.map(renderNavLink)}</div>
      </nav>

      <main class="shell-content">{children}</main>
    </div>
  );
}
