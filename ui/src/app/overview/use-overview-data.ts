import { useQuery } from "@tanstack/react-query";
import { 
  getQueryTypes, 
  getAverageDuration, 
  getQueryRate,
  getQueryLatencyTrends,
  getQueryStatusDistribution,
  getQueryThroughputAnalysis,
  getQueryErrorAnalysis,
  getRecentQueries
} from "@/api/queries";
import { 
  QueryTypesResponse, 
  AverageDurationResponse, 
  QueryRateResponse,
  DateRange,
  QueryLatencyTrendsResult,
  QueryStatusDistributionResult,
  QueryThroughputAnalysisResult,
  QueryErrorAnalysisResult,
  PagedResult,
  RecentQuery,
  TableState
} from "@/lib/types";

interface OverviewData {
  // Key metrics data
  queryTypes: QueryTypesResponse | undefined;
  averageDuration: AverageDurationResponse | undefined;
  queryRate: QueryRateResponse | undefined;
  // Query analysis data
  queryStatusDistribution: QueryStatusDistributionResult[] | undefined;
  queryLatencyTrends: QueryLatencyTrendsResult[] | undefined;
  queryThroughputAnalysis: QueryThroughputAnalysisResult[] | undefined;
  queryErrorAnalysis: QueryErrorAnalysisResult[] | undefined;
  // Recent queries data
  recentQueries: PagedResult<RecentQuery> | undefined;
}

export function useOverviewData(dateRange: DateRange | undefined, tableState?: TableState) {
  const queryEnabled = Boolean(dateRange?.from && dateRange?.to);
  const from = dateRange?.from?.toISOString();
  const to = dateRange?.to?.toISOString();

  // Key metrics queries
  const {
    data: queryTypes,
    isLoading: isLoadingMetrics,
    error: metricsError
  } = useQuery<QueryTypesResponse>({
    queryKey: ['queryTypes', from, to],
    queryFn: () => getQueryTypes(from, to),
    enabled: queryEnabled,
  });

  const {
    data: averageDuration,
    isLoading: isLoadingAvgDuration,
    error: avgDurationError
  } = useQuery<AverageDurationResponse>({
    queryKey: ['averageDuration', from, to],
    queryFn: () => getAverageDuration(from, to),
    enabled: queryEnabled,
  });

  const {
    data: queryRate,
    isLoading: isLoadingRate,
    error: rateError
  } = useQuery<QueryRateResponse>({
    queryKey: ['queryRate', from, to],
    queryFn: () => getQueryRate(from, to),
    enabled: queryEnabled,
  });

  // Query analysis queries
  const {
    data: queryStatusDistribution,
    isLoading: isLoadingAnalysis,
    error: analysisError
  } = useQuery<QueryStatusDistributionResult[]>({
    queryKey: ['queryStatusDistribution', from, to],
    queryFn: () => getQueryStatusDistribution(from, to),
    enabled: queryEnabled,
  });

  const {
    data: queryLatencyTrends,
    isLoading: isLoadingLatency,
    error: latencyError
  } = useQuery<QueryLatencyTrendsResult[]>({
    queryKey: ['queryLatencyTrends', from, to],
    queryFn: () => getQueryLatencyTrends(from, to),
    enabled: queryEnabled,
  });

  const {
    data: queryThroughputAnalysis,
    isLoading: isLoadingThroughput,
    error: throughputError
  } = useQuery<QueryThroughputAnalysisResult[]>({
    queryKey: ['queryThroughputAnalysis', from, to],
    queryFn: () => getQueryThroughputAnalysis(from, to),
    enabled: queryEnabled,
  });

  const {
    data: queryErrorAnalysis,
    isLoading: isLoadingError,
    error: errorAnalysisError
  } = useQuery<QueryErrorAnalysisResult[]>({
    queryKey: ['queryErrorAnalysis', from, to],
    queryFn: () => getQueryErrorAnalysis(from, to),
    enabled: queryEnabled,
  });

  // Recent queries
  const {
    data: recentQueries,
    isLoading: isLoadingRecent,
    error: recentError
  } = useQuery<PagedResult<RecentQuery>>({
    queryKey: ['recentQueries', from, to, tableState],
    queryFn: () => getRecentQueries(
      from, 
      to, 
      tableState?.page || 1,
      tableState?.pageSize || 10,
      tableState?.sortBy || 'timestamp',
      tableState?.sortOrder || 'desc',
      tableState?.filter || ''
    ),
    enabled: queryEnabled,
  });

  const isLoading = 
    isLoadingMetrics || 
    isLoadingAvgDuration || 
    isLoadingRate ||
    isLoadingAnalysis ||
    isLoadingLatency ||
    isLoadingThroughput ||
    isLoadingError ||
    isLoadingRecent;

  const error = 
    metricsError || 
    avgDurationError || 
    rateError ||
    analysisError ||
    latencyError ||
    throughputError ||
    errorAnalysisError ||
    recentError;

  return {
    data: {
      // Key metrics
      queryTypes,
      averageDuration,
      queryRate,
      // Query analysis
      queryStatusDistribution,
      queryLatencyTrends,
      queryThroughputAnalysis,
      queryErrorAnalysis,
      // Recent queries
      recentQueries,
    } as OverviewData,
    isLoading,
    error,
  };
} 