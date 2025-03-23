import { Route, Switch, Redirect } from "wouter";
import Layout from "./components/layout";
import { Overview } from "@/app/overview/index";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { ReactQueryDevtools } from "@tanstack/react-query-devtools";
import { DateRangeProvider } from "@/contexts/date-range-context";

const queryClient = new QueryClient({
  defaultOptions: {
    queries: {
      staleTime: 5 * 60 * 1000, // 5 minutes
      refetchOnWindowFocus: false,
    },
  },
});

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
    <QueryClientProvider client={queryClient}>
      <DateRangeProvider>
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
        <ReactQueryDevtools initialIsOpen={false} />
      </DateRangeProvider>
    </QueryClientProvider>
  );
}

export default App;