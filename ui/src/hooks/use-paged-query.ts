import { useQuery, UseQueryOptions } from "@tanstack/react-query";
import { PagedResult } from "@/lib/types";

// usePagedQuery is the single fetch seam for all paginated endpoints.
// When the count/data split lands (separate /count endpoint + data-only fetch),
// change this hook once and every consumer inherits it automatically.
export function usePagedQuery<T>(
  queryKey: unknown[],
  queryFn: () => Promise<PagedResult<T>>,
  options?: Omit<UseQueryOptions<PagedResult<T>>, "queryKey" | "queryFn">,
) {
  return useQuery<PagedResult<T>>({
    queryKey,
    queryFn,
    ...options,
  });
}
