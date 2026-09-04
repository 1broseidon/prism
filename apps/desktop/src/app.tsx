import { useEffect } from "preact/hooks";
import * as api from "./api";
import { loadAll, subscribeEvents } from "./events";
import { AgentsScreen } from "./screens/Agents";
import { NowScreen } from "./screens/Now";
import { RulesScreen } from "./screens/Rules";
import { ServersScreen } from "./screens/Servers";
import { errorMessage, pending, status, tab } from "./state";
import { Button, Notice } from "./ui";

const TABS = [
  { id: "now", label: "Now" },
  { id: "servers", label: "Servers" },
  { id: "agents", label: "Agents" },
  { id: "rules", label: "Rules" },
] as const;

export function App() {
  useEffect(() => {
    void loadAll();
    let stop: (() => void) | undefined;
    void subscribeEvents().then((unlisten) => {
      stop = unlisten;
    });
    const onKey = (event: KeyboardEvent) => {
      if (event.key === "Escape") void api.hidePanel();
    };
    window.addEventListener("keydown", onKey);
    return () => {
      stop?.();
      window.removeEventListener("keydown", onKey);
    };
  }, []);

  const st = status.value;
  const waiting = pending.value.length + (st?.pending_agents ?? 0);

  return (
    <>
      <header class="header">
        <h1 class="wordmark">Prism</h1>
        <span class="status" title={st?.listening ? "Gateway listening" : "Gateway not listening"}>
          <span class={`dot ${st ? (st.listening ? "ok" : "danger") : ""}`} />
          {st ? `:${st.listen_port}` : "…"}
        </span>
        <span class="spacer" />
        <kbd>esc</kbd>
        <Button variant="icon" aria-label="Hide panel" onClick={() => void api.hidePanel()}>
          ×
        </Button>
      </header>
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
      <main class="body">
        {errorMessage.value ? (
          <Notice
            text={errorMessage.value}
            onDismiss={() => {
              errorMessage.value = null;
            }}
          />
        ) : null}
        {tab.value === "now" ? <NowScreen /> : null}
        {tab.value === "servers" ? <ServersScreen /> : null}
        {tab.value === "agents" ? <AgentsScreen /> : null}
        {tab.value === "rules" ? <RulesScreen /> : null}
      </main>
    </>
  );
}
