import { useQuery } from "@tanstack/react-query";
import { 
  getQueryTypes, 
  getAverageDuration, 
  getQueryRate,
  getQueryLatencyTrends,
  getQueryStatusDistribution,
  getQueryThroughputAnalysis,
  getQueryErrorAnalysis
} from "@/api/queries";
import { 
  QueryTypesResponse, 
  AverageDurationResponse, 
  QueryRateResponse,
  DateRange,
  QueryLatencyTrendsResult,
  QueryStatusDistributionResult,
  QueryThroughputAnalysisResult,
  QueryErrorAnalysisResult
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
}

export function useOverviewData(dateRange: DateRange | undefined) {
  const queryEnabled = Boolean(dateRange?.from && dateRange?.to);
  const from = dateRange?.from?.toISOString();
  const to = dateRange?.to?.toISOString();

  const { 
    data: queryTypes, 
    isLoading: isLoadingQueryTypes,
    error: queryTypesError 
  } = useQuery<QueryTypesResponse>({
    queryKey: ['queryTypes', from, to],
    queryFn: () => getQueryTypes(from, to),
    enabled: queryEnabled,
  });

  const { 
    data: averageDuration, 
    isLoading: isLoadingAverageDuration,
    error: averageDurationError 
  } = useQuery<AverageDurationResponse>({
    queryKey: ['averageDuration', from, to],
    queryFn: () => getAverageDuration(from, to),
    enabled: queryEnabled,
  });

  const { 
    data: queryRate, 
    isLoading: isLoadingQueryRate,
    error: queryRateError 
  } = useQuery<QueryRateResponse>({
    queryKey: ['queryRate', from, to],
    queryFn: () => getQueryRate(from, to),
    enabled: queryEnabled,
  });

  // Query analysis queries
  const { 
    data: queryStatusDistribution,
    isLoading: isLoadingStatus,
    error: errorStatus
  } = useQuery<QueryStatusDistributionResult[]>({
    queryKey: ['queryStatusDistribution', from, to],
    queryFn: () => getQueryStatusDistribution(from, to),
    enabled: queryEnabled,
  });

  const { 
    data: queryLatencyTrends,
    isLoading: isLoadingLatency,
    error: errorLatency
  } = useQuery<QueryLatencyTrendsResult[]>({
    queryKey: ['queryLatencyTrends', from, to],
    queryFn: () => getQueryLatencyTrends(from, to),
    enabled: queryEnabled,
  });

  const { 
    data: queryThroughputAnalysis,
    isLoading: isLoadingThroughput,
    error: errorThroughput
  } = useQuery<QueryThroughputAnalysisResult[]>({
    queryKey: ['queryThroughputAnalysis', from, to],
    queryFn: () => getQueryThroughputAnalysis(from, to),
    enabled: queryEnabled,
  });

  const { 
    data: queryErrorAnalysis,
    isLoading: isLoadingErrorAnalysis,
    error: errorErrorAnalysis
  } = useQuery<QueryErrorAnalysisResult[]>({
    queryKey: ['queryErrorAnalysis', from, to],
    queryFn: () => getQueryErrorAnalysis(from, to),
    enabled: queryEnabled,
  });

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
    } as OverviewData,
    isLoading: 
      isLoadingQueryTypes || 
      isLoadingAverageDuration || 
      isLoadingQueryRate ||
      isLoadingStatus ||
      isLoadingLatency ||
      isLoadingThroughput ||
      isLoadingErrorAnalysis,
    error: 
      queryTypesError || 
      averageDurationError || 
      queryRateError ||
      errorStatus ||
      errorLatency ||
      errorThroughput ||
      errorErrorAnalysis,
  };
} 