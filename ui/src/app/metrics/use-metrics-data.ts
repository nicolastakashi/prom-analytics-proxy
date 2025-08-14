import { useQuery } from "@tanstack/react-query";
import { getMetricQueryPerformanceStatistics, getMetricStatistics, getSeriesMetadata, getSerieExpressions, getMetricUsage, getJobs } from "@/api/metrics";
import { PagedResult, TableState, MetricMetadata, MetricStatistics, MetricQueryPerformanceStatistics, QueryLatencyTrendsResult, DateRange } from "@/lib/types";
import { getQueryLatencyTrends } from "@/api/queries";

interface MetricsData {
  metrics: PagedResult<MetricMetadata> | undefined;
  jobs: string[] | undefined;
}

interface MetricStatisticsData {
  statistics: MetricStatistics | undefined;
}

interface MetricUsageResponse {
  total: number;
  totalPages: number;
  data: Array<{
    id?: string;
    name: string;
    url?: string;
    groupName?: string;
    expression?: string;
  }>;
}

export function useSeriesMetadataTable(tableState?: TableState, searchQuery?: string, unused?: boolean, job?: string) {
  const {
    data: metrics,
    isLoading,
    error
  } = useQuery<PagedResult<MetricMetadata>>({
    queryKey: ['metrics', tableState, searchQuery, unused, job],
    queryFn: () => getSeriesMetadata(
      tableState?.page || 1,
      tableState?.pageSize || 10,
      tableState?.sortBy || 'name',
      tableState?.sortOrder || 'asc',
      searchQuery || '',
      tableState?.type || 'all',
      unused || false,
      job,
    ),
  });

  const { data: jobs } = useQuery<string[]>({
    queryKey: ['jobs'],
    queryFn: getJobs,
  });

  return {
    data: {
      metrics,
      jobs,
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

export function useMetricUsage(
  metricName: string, 
  kind: string, 
  page: number = 1, 
  pageSize: number = 10,
  from?: Date,
  to?: Date
) {
  const fromParam = from ? from.toISOString() : "";
  const toParam = to ? to.toISOString() : "";
  
  return useQuery<MetricUsageResponse, Error>({
    queryKey: ['metric-usage', metricName, kind, page, pageSize, fromParam, toParam],
    queryFn: () => getMetricUsage(
      metricName,
      kind,
      page,
      pageSize,
      fromParam,
      toParam
    ),
    enabled: !!metricName && !!kind,
  });
}

export function useSerieExpressions(metricName: string, tableState?: TableState, timeRange?: DateRange | undefined) {
  const from = timeRange?.from?.toISOString() || "";
  const to = timeRange?.to?.toISOString() || "";
  const { data, isLoading, error } = useQuery({
    queryKey: ['serie-expressions', metricName, tableState, from, to],
    queryFn: () => getSerieExpressions(
      metricName,
      tableState?.page || 1,
      tableState?.pageSize || 10,
      from,
      to
    ),
    enabled: !!metricName,
  });

  return {
    data,
    isLoading,
    error,
  };
}
