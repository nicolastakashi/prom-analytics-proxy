import { Component, ErrorInfo, ReactNode, useEffect } from "react";
import { toast } from "sonner";

interface Props {
  children: ReactNode;
  fallback?: ReactNode;
}

interface State {
  hasError: boolean;
  error: Error | null;
}

export class ErrorBoundary extends Component<Props, State> {
  public state: State = {
    hasError: false,
    error: null,
  };

  public static getDerivedStateFromError(error: Error): State {
    return { hasError: true, error };
  }

  public componentDidCatch(error: Error, errorInfo: ErrorInfo) {
    console.error("Uncaught error:", error, errorInfo);
    toast.error("Something went wrong", {
      description: error.message || "An unexpected error occurred",
    });
  }

  public render() {
    if (this.state.hasError) {
      return (
        this.props.fallback || (
          <div className="flex h-[50vh] w-full items-center justify-center">
            <div className="text-center">
              <h2 className="text-2xl font-bold text-red-600">
                Something went wrong
              </h2>
              <p className="mt-2 text-gray-600">{this.state.error?.message}</p>
            </div>
          </div>
        )
      );
    }

    return this.props.children;
  }
}

// Wrapper component that uses Sonner for error notifications
export function ErrorBoundaryWithToast({ children }: { children: ReactNode }) {
  useEffect(() => {
    const handleError = (event: ErrorEvent) => {
      toast.error("Error", {
        description: event.error?.message || "An unexpected error occurred",
      });
    };

    window.addEventListener("error", handleError);
    return () => window.removeEventListener("error", handleError);
  }, []);

  return <ErrorBoundary>{children}</ErrorBoundary>;
}
