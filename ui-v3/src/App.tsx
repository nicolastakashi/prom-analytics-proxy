import { Route, Switch, Redirect } from "wouter";
import Layout from "./components/layout";
import { Overview } from "@/app/overview";
import { Performance } from "@/app/performance";
import { MetricsExplorer } from "@/app/metrics_explorer";

const routes = [
  {
    path: "/",
    component: Overview,
  },
  {
    path: "/performance",
    component: Performance,
  },
  {
    path: "/metrics",
    component: MetricsExplorer,
  },
] as const;

export type RoutePath = typeof routes[number]["path"];

function App() {
  return (
    <Layout>
      <Switch>
        {routes.map(({ path, component: Component }) => (
          <Route key={path} path={path} component={Component} />
        ))}
        
        {/* Redirect any unknown routes to Overview */}
        <Route>
          <Redirect to="/" />
        </Route>
      </Switch>
    </Layout>
  );
}

export default App;