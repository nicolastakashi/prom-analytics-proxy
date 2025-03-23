import { Route, Switch, Link, Redirect } from "wouter";
import { Overview } from "./pages/overview";
import { Performance } from "./pages/performance";
import { MetricsExplorer } from "./pages/metrics_explorer";

function App() {
  return (
    <div>
      {/* Navigation */}
      <nav>
        <Link href="/">Overview</Link>
        <Link href="/performance">Performance</Link>
        <Link href="/metrics">Metrics Explorer</Link>
      </nav>

      {/* Routes */}
      <Switch>
        <Route path="/" component={Overview} />
        <Route path="/performance" component={Performance} />
        <Route path="/metrics" component={MetricsExplorer} />
        
        {/* Optional: Redirect any unknown routes to Overview */}
        <Route>
          <Redirect to="/" />
        </Route>
      </Switch>
    </div>
  );
}

export default App;