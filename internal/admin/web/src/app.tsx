import { LocationProvider, Router, Route } from "preact-iso";
import { Layout } from "./components/Layout";
import { Toaster } from "./components/Toaster";
import { Overview } from "./pages/Overview";
import { Servers } from "./pages/Servers";
import { ServerDetail } from "./pages/ServerDetail";
import { Identity } from "./pages/Identity";
import { AgentDetail } from "./pages/AgentDetail";
import { GroupDetail } from "./pages/GroupDetail";
import { Audit } from "./pages/Audit";

export function App() {
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
