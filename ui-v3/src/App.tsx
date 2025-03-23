import { Route, Switch, Redirect } from "wouter";
import Layout from "./components/layout";
import { Overview } from "@/app/overview/index";

const routes = [
  {
    path: "/",
    component: Overview,
  },
  {
    path: "/performance",
    component: () => <div>Performance</div>,
  },
  {
    path: "/metrics",
    component: () => <div>Metrics</div>,
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