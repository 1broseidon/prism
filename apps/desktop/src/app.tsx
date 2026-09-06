import { useEffect } from "preact/hooks";
import * as api from "./api";
import { loadAll, loadUpdateStatus, subscribeEvents } from "./events";
import { AddServerScreen } from "./screens/AddServer";
import { AgentScreen } from "./screens/Agent";
import { AgentToolsScreen } from "./screens/AgentTools";
import { ActivityScreen } from "./screens/Activity";
import { AgentsScreen } from "./screens/Agents";
import { ConnectAgentScreen } from "./screens/ConnectAgent";
import { HostScreen } from "./screens/Host";
import { hostName } from "./hosts";
import { NowScreen } from "./screens/Now";
import { RulesScreen } from "./screens/Rules";
import { ServersScreen } from "./screens/Servers";
import { SettingsScreen } from "./screens/Settings";
import { agents, errorMessage, pending, pop, push, servers, stack, status, tab, update } from "./state";
import type { Screen } from "./state";
import { Button, Notice } from "./ui";

const TABS = [
  { id: "now", label: "Now" },
  { id: "servers", label: "Servers" },
  { id: "agents", label: "Agents" },
  { id: "rules", label: "Rules" },
] as const;

function titleOf(screen: Screen): string {
  switch (screen.kind) {
    case "add-server":
      return "Add server";
    case "connect-agent":
      return "Connect an agent";
    case "settings":
      return "Settings";
    case "agent":
      return agents.value.find((a) => a.id === screen.agentId)?.name ?? "Agent";
    case "agent-server":
      return servers.value.find((s) => s.id === screen.serverId)?.name ?? "Server";
    case "host":
      return agents.value.find((a) => a.id === screen.agentId)?.name ?? hostName(screen.agentId);
    case "activity":
      return screen.agentId ? (agents.value.find((a) => a.id === screen.agentId)?.name ?? hostName(screen.agentId)) : "Actions";
  }
}

/** The mark: two facets of a standing prism. Same polygons as src-tauri/icons/prism-mark.svg. */
const Mark = () => (
  <svg class="mark" viewBox="8 12 48 38" width="22" height="18" aria-hidden="true" fill="currentColor">
    <polygon points="8,50 30,12 38,50" />
    <polygon points="42,50 30,12 52,18 56,50" />
  </svg>
);

const SlidersIcon = () => (
  <svg viewBox="0 0 16 16" width="16" height="16" aria-hidden="true">
    <path d="M2 4.5h12M2 11.5h12" stroke="currentColor" stroke-width="1.6" stroke-linecap="round" fill="none" />
    <circle cx="6" cy="4.5" r="2" fill="var(--color-paper)" stroke="currentColor" stroke-width="1.6" />
    <circle cx="10.5" cy="11.5" r="2" fill="var(--color-paper)" stroke="currentColor" stroke-width="1.6" />
  </svg>
);

export function App() {
  useEffect(() => {
    void loadAll();
    void loadUpdateStatus();
    let stop: (() => void) | undefined;
    void subscribeEvents().then((unlisten) => {
      stop = unlisten;
    });
    const onKey = (event: KeyboardEvent) => {
      if (event.key !== "Escape") return;
      // Escape walks back one screen at a time; only from a tab does it hide the panel.
      if (stack.value.length > 0) pop();
      else void api.hidePanel();
    };
    window.addEventListener("keydown", onKey);
    return () => {
      stop?.();
      window.removeEventListener("keydown", onKey);
    };
  }, []);

  const st = status.value;
  const waiting = pending.value.length + (st?.pending_agents ?? 0) + (st?.pending_signins ?? 0);
  const top = stack.value[stack.value.length - 1];

  return (
    <>
      <header class="header">
        {top ? (
          <>
            <Button variant="icon" class="back" aria-label="Back" title="Back (Esc)" onClick={pop}>
              ←
            </Button>
            <h1 class="screen-title truncate">{titleOf(top)}</h1>
          </>
        ) : (
          <>
            <Mark />
            <h1 class="wordmark">Prism</h1>
            <button
              type="button"
              class="status"
              title={st?.listening ? "Listening. Connect an agent." : "Not listening"}
              onClick={() => push({ kind: "connect-agent" })}
            >
              <span class={`dot ${st ? (st.listening ? "ok" : "danger") : ""}`} />
              {st ? `:${st.listen_port}` : "…"}
            </button>
          </>
        )}
        <span class="spacer" />
        {top ? null : (
          <Button
            variant="icon"
            class={update.value ? "has-update" : ""}
            aria-label={update.value ? `Settings, update to ${update.value.version} available` : "Settings"}
            title={update.value ? `Prism ${update.value.version} is ready` : "Settings"}
            onClick={() => push({ kind: "settings" })}
          >
            <SlidersIcon />
          </Button>
        )}
        <Button variant="icon" aria-label="Hide panel" title="Hide" onClick={() => void api.hidePanel()}>
          ×
        </Button>
      </header>
      {top ? null : (
        <nav class="tabs" role="tablist" aria-label="Sections">
          {TABS.map((t) => (
            <button
              key={t.id}
              type="button"
              role="tab"
              class="tab"
              aria-selected={tab.value === t.id}
              onClick={() => {
                tab.value = t.id;
              }}
            >
              {t.label}
              {t.id === "now" && waiting > 0 ? <span class="count">{waiting}</span> : null}
            </button>
          ))}
        </nav>
      )}
      {errorMessage.value ? (
        <Notice
          text={errorMessage.value}
          onDismiss={() => {
            errorMessage.value = null;
          }}
        />
      ) : null}
      <main class="body" key={top ? `${JSON.stringify(top)}:${stack.value.length}` : tab.value}>
        {top?.kind === "add-server" ? <AddServerScreen /> : null}
        {top?.kind === "connect-agent" ? <ConnectAgentScreen /> : null}
        {top?.kind === "settings" ? <SettingsScreen /> : null}
        {top?.kind === "agent" ? <AgentScreen agentId={top.agentId} /> : null}
        {top?.kind === "host" ? <HostScreen agentId={top.agentId} /> : null}
        {top?.kind === "activity" ? <ActivityScreen filter={top} /> : null}
        {top?.kind === "agent-server" ? <AgentToolsScreen agentId={top.agentId} serverId={top.serverId} /> : null}
        {!top && tab.value === "now" ? <NowScreen /> : null}
        {!top && tab.value === "servers" ? <ServersScreen /> : null}
        {!top && tab.value === "agents" ? <AgentsScreen /> : null}
        {!top && tab.value === "rules" ? <RulesScreen /> : null}
      </main>
    </>
  );
}
