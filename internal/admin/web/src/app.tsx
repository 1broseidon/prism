import { LocationProvider, Router, Route } from "preact-iso";
import { Layout } from "./components/Layout";
import { Toaster } from "./components/Toaster";
import { me } from "./state/me";
import { Overview } from "./pages/Overview";
import { Servers } from "./pages/Servers";
import { ServerDetail } from "./pages/ServerDetail";
import { Agents } from "./pages/Agents";
import { AgentDetail } from "./pages/AgentDetail";
import { Policy } from "./pages/Policy";
import { GroupDetail } from "./pages/GroupDetail";
import { Audit } from "./pages/Audit";
import { SettingsNetwork } from "./pages/SettingsNetwork";
import { SettingsWorkspaces } from "./pages/SettingsWorkspaces";
import { WorkspaceDetail } from "./pages/WorkspaceDetail";
import { SettingsSignIn } from "./pages/SettingsSignIn";
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
          <Route path="/agents" component={Agents} />
          <Route path="/agents/:prismId" component={AgentDetail} />
          <Route path="/policy" component={Policy} />
          <Route path="/policy/groups/:name" component={GroupDetail} />
          <Route path="/activity" component={Audit} />
          <Route path="/settings/network" component={SettingsNetwork} />
          <Route path="/settings/workspaces" component={SettingsWorkspaces} />
          <Route path="/settings/workspaces/:id" component={WorkspaceDetail} />
          <Route
            path="/settings/sign-in"
            component={SettingsSignIn}
          />
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
