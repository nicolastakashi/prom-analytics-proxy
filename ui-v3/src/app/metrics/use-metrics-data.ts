import { useQuery } from "@tanstack/react-query";
import { getMetricQueryPerformanceStatistics, getMetricStatistics, getSeriesMetadata } from "@/api/metrics";
import { PagedResult, TableState, MetricMetadata, MetricStatistics, MetricQueryPerformanceStatistics, QueryLatencyTrendsResult } from "@/lib/types";
import { DateRange } from "react-day-picker";
import { getQueryLatencyTrends } from "@/api/queries";

interface MetricsData {
  metrics: PagedResult<MetricMetadata> | undefined;
}

interface MetricStatisticsData {
  statistics: MetricStatistics | undefined;
}

export function useSeriesMetadataTable(tableState?: TableState, searchQuery?: string) {
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

export function useMetricStatistics(metricName: string, timeRange: DateRange | undefined) {
  const from = timeRange?.from?.toISOString() || "";
  const to = timeRange?.to?.toISOString() || "";

  const {
    data: statistics,
    isLoading,
    error
  } = useQuery<MetricStatistics>({
    queryKey: ['metricStatistics', metricName, from, to],
    queryFn: () => getMetricStatistics(metricName, from, to),
  });

  return {
    data: {
      statistics,
    } as MetricStatisticsData,
    isLoading,
    error,
  };
}

export function useMetricQueryPerformanceStatistics(metricName: string, timeRange: DateRange | undefined) {
  const from = timeRange?.from?.toISOString() || "";
  const to = timeRange?.to?.toISOString() || "";

  const { data, isLoading, error } = useQuery<MetricQueryPerformanceStatistics>({
    queryKey: ['metricQueryPerformanceStatistics', metricName, from, to],
    queryFn: () => getMetricQueryPerformanceStatistics(metricName, from, to),
  });

  return {
    data,
    isLoading,
    error,
  };
}

export function useQueryLatencyTrends(metricName: string, timeRange: DateRange | undefined) {
  const from = timeRange?.from?.toISOString() || "";
  const to = timeRange?.to?.toISOString() || "";

  const { data, isLoading, error } = useQuery<QueryLatencyTrendsResult[]>({
    queryKey: ['queryLatencyTrends', metricName, from, to],
    queryFn: () => getQueryLatencyTrends(from, to, metricName),
  });

  return {
    data,
    isLoading,
    error,
  };
}
