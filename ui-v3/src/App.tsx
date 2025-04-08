import { Route, Switch, Redirect } from "wouter";
import Layout from "./components/layout";
import { Overview } from "@/app/overview/index";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { ReactQueryDevtools } from "@tanstack/react-query-devtools";
import { DateRangeProvider } from "@/contexts/date-range-context";
import { Toaster } from "@/components/ui/sonner";
import { ErrorBoundaryWithToast } from "@/components/error-boundary";
import { useLocation } from "wouter";
import MetricsExplorer from "./app/metrics";

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
    breadcrumb: {
      current: "Overview"
    }
  },
  {
    path: "/performance",
    component: () => <div>Performance</div>,
    breadcrumb: {
      current: "Performance"
    }
  },
  {
    path: "/metrics",
    component: () => <MetricsExplorer />,
    breadcrumb: {
      current: "Metrics"
    }
  },
] as const;

export type RoutePath = typeof routes[number]["path"];

function App() {
  const [location] = useLocation();
  const currentRoute = routes.find(route => route.path === location) || routes[0];

  return (
    <ErrorBoundaryWithToast>
      <QueryClientProvider client={queryClient}>
        <DateRangeProvider>
          <Layout breadcrumb={currentRoute.breadcrumb}>
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
          <Toaster />
        </DateRangeProvider>
      </QueryClientProvider>
    </ErrorBoundaryWithToast>
  );
}

export default App;