import type { ComponentChildren } from "preact";
import { useLocation } from "preact-iso";
import { info } from "../state";
import { fmtUptime } from "../util/time";

interface Props {
  children: ComponentChildren;
}

const NAV_ITEMS = [
  { href: "/", label: "Overview" },
  { href: "/servers", label: "MCP Servers" },
  { href: "/identity", label: "Identity" },
  { href: "/audit", label: "Audit" },
];

export function Layout({ children }: Props) {
  const loc = useLocation();
  const path = loc.path || "/";
  const i = info.data.value;
  const err = info.error.value;

  return (
    <div class="shell">
      <div class="shell-logo">
        <h1>prism</h1>
      </div>

      <header class="shell-header">
        <div class="status-badge">
          <div class={err ? "status-dot error" : "status-dot"} />
          <span class="status-text">{err ? "disconnected" : "running"}</span>
        </div>
        <div class="shell-meta">
          {i ? <span>v{i.version}</span> : null}
          {i ? <span>up {fmtUptime(i.uptime)}</span> : null}
          {i ? <span>{i.goroutines} goroutines</span> : null}
        </div>
      </header>

      <nav class="shell-nav">
        <div class="nav-section-label">Console</div>
        {NAV_ITEMS.map((n) => {
          const active = path === n.href || (n.href !== "/" && path.startsWith(n.href));
          // Plain anchors — preact-iso's LocationProvider installs a global
          // click listener that intercepts in-origin <a> clicks. No custom
          // onClick needed; it correctly handles modifier keys itself.
          return (
            <a
              key={n.href}
              href={n.href}
              class={active ? "nav-link active" : "nav-link"}
            >
              {n.label}
            </a>
          );
        })}
      </nav>

      <main class="shell-content">{children}</main>
    </div>
  );
}
