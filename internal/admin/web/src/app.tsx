import { LocationProvider, Router, Route } from "preact-iso";
import { Layout } from "./components/Layout";
import { Overview } from "./pages/Overview";
import { Servers } from "./pages/Servers";
import { Identity } from "./pages/Identity";
import { Audit } from "./pages/Audit";

export function App() {
  return (
    <LocationProvider>
      <Layout>
        <Router>
          <Route path="/" component={Overview} />
          <Route path="/servers" component={Servers} />
          <Route path="/identity" component={Identity} />
          <Route path="/audit" component={Audit} />
          <Route default component={NotFound} />
        </Router>
      </Layout>
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
