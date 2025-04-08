import { useQuery } from "@tanstack/react-query";
import { getSeriesMetadata } from "@/api/metrics";
import { PagedResult, TableState, MetricMetadata } from "@/lib/types";

interface MetricsData {
  metrics: PagedResult<MetricMetadata> | undefined;
}

export function useMetricsData(tableState?: TableState, searchQuery?: string) {
  const {
    data: metrics,
    isLoading,
    error
  } = useQuery<PagedResult<MetricMetadata>>({
    queryKey: ['metrics', tableState, searchQuery],
    queryFn: () => getSeriesMetadata(
      tableState?.page || 1,
      tableState?.pageSize || 10,
      tableState?.sortBy || 'name',
      tableState?.sortOrder || 'asc',
      searchQuery || '',
      tableState?.type || 'all'
    ),
  });

  return {
    data: {
      metrics,
    } as MetricsData,
    isLoading,
    error,
  };
} 