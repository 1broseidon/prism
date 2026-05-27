import type { ComponentChildren } from "preact";
import { useEffect, useRef, useState } from "preact/hooks";
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
  icon: preact.JSX.Element;
  // When the item should match a broader route prefix than its exact href —
  // e.g., Settings links to /settings/network but should stay active across
  // /settings/storage, /settings/sign-in, /settings/storage/:id, etc.
  matchPrefix?: string;
}

// Small mono-stroke icons. 14px viewport, currentColor stroke.
const Icon = {
  overview: (
    <svg width="14" height="14" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.75" stroke-linecap="round" stroke-linejoin="round" aria-hidden="true">
      <rect x="3" y="3" width="7" height="9" />
      <rect x="14" y="3" width="7" height="5" />
      <rect x="14" y="12" width="7" height="9" />
      <rect x="3" y="16" width="7" height="5" />
    </svg>
  ),
  servers: (
    <svg width="14" height="14" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.75" stroke-linecap="round" stroke-linejoin="round" aria-hidden="true">
      <rect x="3" y="4" width="18" height="6" rx="1" />
      <rect x="3" y="14" width="18" height="6" rx="1" />
      <line x1="7" y1="7" x2="7.01" y2="7" />
      <line x1="7" y1="17" x2="7.01" y2="17" />
    </svg>
  ),
  agents: (
    <svg width="14" height="14" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.75" stroke-linecap="round" stroke-linejoin="round" aria-hidden="true">
      <path d="M17 21v-2a4 4 0 0 0-4-4H5a4 4 0 0 0-4 4v2" />
      <circle cx="9" cy="7" r="4" />
      <path d="M23 21v-2a4 4 0 0 0-3-3.87" />
      <path d="M16 3.13a4 4 0 0 1 0 7.75" />
    </svg>
  ),
  activity: (
    <svg width="14" height="14" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.75" stroke-linecap="round" stroke-linejoin="round" aria-hidden="true">
      <polyline points="22 12 18 12 15 21 9 3 6 12 2 12" />
    </svg>
  ),
  policy: (
    <svg width="14" height="14" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.75" stroke-linecap="round" stroke-linejoin="round" aria-hidden="true">
      <path d="M12 22s8-4 8-10V5l-8-3-8 3v7c0 6 8 10 8 10z" />
    </svg>
  ),
  settings: (
    <svg width="14" height="14" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.75" stroke-linecap="round" stroke-linejoin="round" aria-hidden="true">
      <circle cx="12" cy="12" r="3" />
      <path d="M19.4 15a1.65 1.65 0 0 0 .33 1.82l.06.06a2 2 0 0 1 0 2.83 2 2 0 0 1-2.83 0l-.06-.06a1.65 1.65 0 0 0-1.82-.33 1.65 1.65 0 0 0-1 1.51V21a2 2 0 0 1-2 2 2 2 0 0 1-2-2v-.09A1.65 1.65 0 0 0 9 19.4a1.65 1.65 0 0 0-1.82.33l-.06.06a2 2 0 0 1-2.83 0 2 2 0 0 1 0-2.83l.06-.06a1.65 1.65 0 0 0 .33-1.82 1.65 1.65 0 0 0-1.51-1H3a2 2 0 0 1-2-2 2 2 0 0 1 2-2h.09A1.65 1.65 0 0 0 4.6 9a1.65 1.65 0 0 0-.33-1.82l-.06-.06a2 2 0 0 1 0-2.83 2 2 0 0 1 2.83 0l.06.06a1.65 1.65 0 0 0 1.82.33H9a1.65 1.65 0 0 0 1-1.51V3a2 2 0 0 1 2-2 2 2 0 0 1 2 2v.09a1.65 1.65 0 0 0 1 1.51 1.65 1.65 0 0 0 1.82-.33l.06-.06a2 2 0 0 1 2.83 0 2 2 0 0 1 0 2.83l-.06.06a1.65 1.65 0 0 0-.33 1.82V9a1.65 1.65 0 0 0 1.51 1H21a2 2 0 0 1 2 2 2 2 0 0 1-2 2h-.09a1.65 1.65 0 0 0-1.51 1z" />
    </svg>
  ),
};

const MONITOR_NAV: NavItem[] = [
  { href: "/", label: "Overview", icon: Icon.overview },
  { href: "/servers", label: "Servers", icon: Icon.servers },
  { href: "/agents", label: "Agents", icon: Icon.agents },
  { href: "/activity", label: "Activity", icon: Icon.activity },
];

const GOVERN_NAV: NavItem[] = [
  { href: "/policy", label: "Policy", icon: Icon.policy },
  {
    href: "/settings/network",
    label: "Settings",
    icon: Icon.settings,
    matchPrefix: "/settings",
  },
];

export function Layout({ children }: Props) {
  const loc = useLocation();
  const path = loc.path || "/";
  const i = info.data.value;
  const m = me.value;
  const [navOpen, setNavOpen] = useState(false);

  useEffect(() => {
    setNavOpen(false);
  }, [path]);

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

  const renderNavLink = (n: NavItem) => {
    const prefix = n.matchPrefix ?? n.href;
    const active =
      path === n.href ||
      (prefix !== "/" &&
        (path === prefix || path.startsWith(prefix + "/")));
    return (
      <a
        key={n.href}
        href={n.href}
        class={active ? "nav-link active" : "nav-link"}
      >
        <span class="nav-link-icon">{n.icon}</span>
        <span class="nav-link-label">{n.label}</span>
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
        <InstanceSwitcher />
      </div>

      <header class="shell-header">
        <Breadcrumb path={path} />
        <div class="shell-header-right">
          {i && <GatewayStatus connected />}
        </div>
      </header>

      <button
        class="nav-scrim"
        aria-label="Close navigation"
        tabindex={-1}
        onClick={() => setNavOpen(false)}
      />

      <nav class="shell-nav" id="primary-nav">
        <div class="nav-groups">
          <div class="nav-section-label">Monitor</div>
          {MONITOR_NAV.map(renderNavLink)}
          <div class="nav-section-label nav-section-label-spaced">Govern</div>
          {GOVERN_NAV.map(renderNavLink)}
        </div>
        <div class="nav-footer">
          {m?.auth === "session" && m.email && (
            <UserMenu email={m.email} role={m.role} onSignOut={signOut} />
          )}
          {i && (
            <div class="nav-meta">
              <span>v{i.version}</span>
              <span class="nav-meta-dot">·</span>
              <span>up {fmtUptime(i.uptime)}</span>
            </div>
          )}
        </div>
      </nav>

      <main class="shell-content">{children}</main>
    </div>
  );
}

// Instance switcher — replaces the decorative wordmark with a workspace
// button. Signals multi-tenancy even when there's only one workspace
// today; the chevron suggests a menu that can hang off it later.
function InstanceSwitcher() {
  return (
    <a class="instance-switcher" href="/" aria-label="prism workspace">
      <div class="instance-mark">P</div>
      <div class="instance-body">
        <div class="instance-name">prism</div>
        <div class="instance-env">production</div>
      </div>
      <svg
        class="instance-chevron"
        width="10"
        height="10"
        viewBox="0 0 24 24"
        fill="none"
        stroke="currentColor"
        stroke-width="2"
        stroke-linecap="round"
        stroke-linejoin="round"
        aria-hidden="true"
      >
        <polyline points="6 9 12 15 18 9" />
      </svg>
    </a>
  );
}

// Breadcrumb derives from the URL. Static segments (servers, agents,
// settings, policy, activity, overview) get readable labels; dynamic
// segments (ULIDs, UUIDs, names) render as-is in mono.
const STATIC_SEGMENT_LABELS: Record<string, string> = {
  "": "overview",
  servers: "servers",
  agents: "agents",
  activity: "activity",
  policy: "policy",
  settings: "settings",
  network: "network",
  storage: "storage",
  "sign-in": "sign-in",
  groups: "groups",
};

function Breadcrumb({ path }: { path: string }) {
  const segments = path.split("/").filter(Boolean);
  if (segments.length === 0) {
    return (
      <nav class="breadcrumb" aria-label="Breadcrumb">
        <span class="breadcrumb-current">overview</span>
      </nav>
    );
  }
  const crumbs = segments.map((seg, idx) => {
    const isLast = idx === segments.length - 1;
    const label = STATIC_SEGMENT_LABELS[seg] ?? seg;
    const isStatic = seg in STATIC_SEGMENT_LABELS;
    const href = "/" + segments.slice(0, idx + 1).join("/");
    if (isLast) {
      return (
        <span
          key={href}
          class={isStatic ? "breadcrumb-current" : "breadcrumb-current breadcrumb-dynamic"}
        >
          {label}
        </span>
      );
    }
    return (
      <>
        <a key={href} href={href} class="breadcrumb-link">
          {label}
        </a>
        <span class="breadcrumb-sep" aria-hidden="true">
          /
        </span>
      </>
    );
  });
  return (
    <nav class="breadcrumb" aria-label="Breadcrumb">
      {crumbs}
    </nav>
  );
}

// Gateway status pip — shown in the header right. Always "connected"
// when this UI is rendering (we're talking to the gateway).
function GatewayStatus({ connected }: { connected: boolean }) {
  return (
    <span class="gateway-status" title={connected ? "Gateway connected" : "Gateway disconnected"}>
      <span class={`status-pip status-pip-${connected ? "ok" : "error"}`} />
      <span class="gateway-status-label">{connected ? "connected" : "offline"}</span>
    </span>
  );
}

// User menu — avatar + email + dropdown anchored to the sidebar footer.
function UserMenu({
  email,
  role,
  onSignOut,
}: {
  email: string;
  role?: string;
  onSignOut: () => void;
}) {
  const [open, setOpen] = useState(false);
  const ref = useRef<HTMLDivElement>(null);

  useEffect(() => {
    if (!open) return;
    const onClickAway = (e: MouseEvent) => {
      if (ref.current && !ref.current.contains(e.target as Node)) {
        setOpen(false);
      }
    };
    const onKey = (e: KeyboardEvent) => {
      if (e.key === "Escape") setOpen(false);
    };
    document.addEventListener("mousedown", onClickAway);
    document.addEventListener("keydown", onKey);
    return () => {
      document.removeEventListener("mousedown", onClickAway);
      document.removeEventListener("keydown", onKey);
    };
  }, [open]);

  const initials = email.slice(0, 2).toUpperCase();
  return (
    <div class="user-menu" ref={ref}>
      <button
        type="button"
        class={open ? "user-menu-trigger user-menu-trigger-open" : "user-menu-trigger"}
        aria-haspopup="menu"
        aria-expanded={open}
        onClick={() => setOpen((v) => !v)}
      >
        <span class="user-avatar" aria-hidden="true">
          {initials}
        </span>
        <span class="user-meta">
          <span class="user-email">{email}</span>
          {role && role !== "admin" && <span class="user-role">{role}</span>}
        </span>
        <svg
          class="user-menu-chevron"
          width="10"
          height="10"
          viewBox="0 0 24 24"
          fill="none"
          stroke="currentColor"
          stroke-width="2"
          stroke-linecap="round"
          stroke-linejoin="round"
          aria-hidden="true"
        >
          <polyline points="6 15 12 9 18 15" />
        </svg>
      </button>
      {open && (
        <div class="user-menu-pop" role="menu">
          <div class="user-menu-pop-header">
            <span class="user-avatar user-avatar-lg" aria-hidden="true">
              {initials}
            </span>
            <div>
              <div class="user-menu-pop-email">{email}</div>
              {role && (
                <div class="user-menu-pop-role">{role || "admin"}</div>
              )}
            </div>
          </div>
          <button
            type="button"
            class="user-menu-pop-item"
            role="menuitem"
            onClick={() => {
              setOpen(false);
              onSignOut();
            }}
          >
            Sign out
          </button>
        </div>
      )}
    </div>
  );
}
