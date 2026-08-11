import { useQuery } from "@tanstack/react-query";
import {
  getMetricQueryPerformanceStatistics,
  getMetricStatistics,
  getSeriesMetadata,
  getSerieExpressions,
  getMetricUsage,
  getProducers,
} from "@/api/metrics";
import {
  PagedResult,
  TableState,
  MetricMetadata,
  MetricStatistics,
  MetricQueryPerformanceStatistics,
  MetricUsageItem,
  QueryLatencyTrendsResult,
  DateRange,
} from "@/lib/types";
import { getQueryLatencyTrends } from "@/api/queries";
import { usePagedQuery } from "@/hooks/use-paged-query";

interface MetricsData {
  metrics: PagedResult<MetricMetadata> | undefined;
  producers: string[] | undefined;
}

interface MetricStatisticsData {
  statistics: MetricStatistics | undefined;
}

export function useSeriesMetadataTable(
  tableState?: TableState,
  searchQuery?: string,
  usage?: "all" | "used" | "unused",
  job?: string,
) {
  const {
    data: metrics,
    isLoading,
    error,
  } = usePagedQuery<MetricMetadata>(
    ["metrics", tableState, searchQuery, usage, job],
    () =>
      getSeriesMetadata(
        tableState?.page || 1,
        tableState?.pageSize || 10,
        tableState?.sortBy || "name",
        tableState?.sortOrder || "asc",
        searchQuery || "",
        tableState?.type || "all",
        usage || "all",
        job,
      ),
  );

  const { data: producers } = useQuery<string[]>({
    queryKey: ["producers"],
    queryFn: getProducers,
  });

  return {
    data: {
      metrics,
      producers,
    } as MetricsData,
    isLoading,
    error,
  };
}

export function useMetricStatistics(
  metricName: string,
  timeRange: DateRange | undefined,
) {
  const from = timeRange?.from?.toISOString() || "";
  const to = timeRange?.to?.toISOString() || "";

  const {
    data: statistics,
    isLoading,
    error,
  } = useQuery<MetricStatistics>({
    queryKey: ["metricStatistics", metricName, from, to],
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

export function useMetricQueryPerformanceStatistics(
  metricName: string,
  timeRange: DateRange | undefined,
) {
  const from = timeRange?.from?.toISOString() || "";
  const to = timeRange?.to?.toISOString() || "";

  const { data, isLoading, error } = useQuery<MetricQueryPerformanceStatistics>(
    {
      queryKey: ["metricQueryPerformanceStatistics", metricName, from, to],
      queryFn: () => getMetricQueryPerformanceStatistics(metricName, from, to),
    },
  );

  return {
    data,
    isLoading,
    error,
  };
}

export function useQueryLatencyTrends(
  metricName: string,
  timeRange: DateRange | undefined,
) {
  const from = timeRange?.from?.toISOString() || "";
  const to = timeRange?.to?.toISOString() || "";

  const { data, isLoading, error } = useQuery<QueryLatencyTrendsResult[]>({
    queryKey: ["queryLatencyTrends", metricName, from, to],
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
  to?: Date,
) {
  const fromParam = from ? from.toISOString() : "";
  const toParam = to ? to.toISOString() : "";

  return usePagedQuery<MetricUsageItem>(
    ["metric-usage", metricName, kind, page, pageSize, fromParam, toParam],
    () => getMetricUsage(metricName, kind, page, pageSize, fromParam, toParam),
    { enabled: !!metricName && !!kind },
  );
}

export function useSerieExpressions(
  metricName: string,
  tableState?: TableState,
  timeRange?: DateRange | undefined,
) {
  const from = timeRange?.from?.toISOString() || "";
  const to = timeRange?.to?.toISOString() || "";
  const { data, isLoading, error } = useQuery({
    queryKey: ["serie-expressions", metricName, tableState, from, to],
    queryFn: () =>
      getSerieExpressions(
        metricName,
        tableState?.page || 1,
        tableState?.pageSize || 10,
        from,
        to,
      ),
    enabled: !!metricName,
  });

  return {
    data,
    isLoading,
    error,
  };
}
