import { Route, Switch, Redirect, useLocation } from "wouter";
import Layout from "./components/layout";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { ReactQueryDevtools } from "@tanstack/react-query-devtools";
import { DateRangeProvider } from "@/contexts/date-range-context";
import { Toaster } from "@/components/ui/sonner";
import { ErrorBoundaryWithToast } from "@/components/error-boundary";
import { routeConfigs, ROUTES } from "@/lib/routes";

const queryClient = new QueryClient({
  defaultOptions: {
    queries: {
      staleTime: 5 * 60 * 1000, // 5 minutes
      refetchOnWindowFocus: false,
    },
  },
});

// Helper function to check if a location matches a route pattern
const matchRoute = (pattern: string, path: string) => {
  // Convert route pattern to regex by replacing :param with a capture group
  const regexPattern = pattern
    .replace(/\\/g, '\\\\') // Escape backslashes
    .replace(/:[^/]+/g, '([^/]+)')
    .replace(/\//g, '\\/');
  
  const regex = new RegExp(`^${regexPattern}$`);
  return regex.test(path);
};

function App() {
  const [location] = useLocation();
  
  // Find the current route by checking each route pattern against the current location
  const currentRoute = routeConfigs.find(route => 
    matchRoute(route.path, location)
  ) || routeConfigs[0]; // Default to first route if no match

  return (
    <ErrorBoundaryWithToast>
      <QueryClientProvider client={queryClient}>
        <DateRangeProvider>
          <Layout breadcrumb={currentRoute.breadcrumb}>
            <Switch>
              {routeConfigs.map(({ path, component: Component }) => (
                <Route key={path} path={path} component={Component} />
              ))}
              
              {/* Redirect any unknown routes to Overview */}
              <Route>
                <Redirect to={ROUTES.HOME} />
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