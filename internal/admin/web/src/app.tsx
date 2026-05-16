import { LocationProvider, Router, Route } from "preact-iso";
import { Layout } from "./components/Layout";
import { Toaster } from "./components/Toaster";
import { me } from "./state/me";
import { Overview } from "./pages/Overview";
import { Servers } from "./pages/Servers";
import { ServerDetail } from "./pages/ServerDetail";
import { Identity } from "./pages/Identity";
import { AgentDetail } from "./pages/AgentDetail";
import { GroupDetail } from "./pages/GroupDetail";
import { Audit } from "./pages/Audit";
import { Config } from "./pages/Config";
import { Login } from "./pages/Login";

export function App() {
  const m = me.value;

  // Until we've fetched /auth/me at least once, show a minimal shell.
  if (m === null) {
    return <div class="boot" />;
  }

  // When auth is required and we're not signed in, the login screen is the
  // only thing the user can reach.
  if (m.auth === "required") {
    return (
      <LocationProvider>
        <Login />
        <Toaster />
      </LocationProvider>
    );
  }

  return (
    <LocationProvider>
      <Layout>
        <Router>
          <Route path="/" component={Overview} />
          <Route path="/servers" component={Servers} />
          <Route path="/servers/:id" component={ServerDetail} />
          <Route path="/identity" component={Identity} />
          <Route path="/identity/agents/:prismId" component={AgentDetail} />
          <Route path="/identity/groups/:name" component={GroupDetail} />
          <Route path="/audit" component={Audit} />
          <Route path="/config" component={Config} />
          <Route default component={NotFound} />
        </Router>
      </Layout>
      <Toaster />
    </LocationProvider>
  );
}

function NotFound() {
  return (
    <div class="page-header">
      <div>
        <div class="page-title">Not found</div>
        <div class="page-subtitle">The page you requested does not exist.</div>
      </div>
    </div>
  );
}
