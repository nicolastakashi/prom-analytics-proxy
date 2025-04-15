import { AverageDurationResponse, PagedResult, QueryErrorAnalysisResult, QueryLatencyTrendsResult, QueryRateResponse, QueryStatusDistributionResult, QueryThroughputAnalysisResult, QueryTypesResponse, RecentQuery } from "@/lib/types"

interface ApiConfig {
  baseUrl: string;
  endpoints: {
    queryTypes: string;
    queryRate: string;
    averageDuration: string;
    queryStatusDistribution: string;
    queryLatencyTrends: string;
    queryThroughputAnalysis: string;
    queryErrorAnalysis: string;
    recentQueries: string;
  };
}

interface FetchOptions {
  from?: string;
  to?: string;
  page?: number;
  pageSize?: number;
  sortBy?: string;
  sortOrder?: string;
  filter?: string;
  metricName?: string;
}

const API_CONFIG: ApiConfig = {
  baseUrl: 'http://localhost:9091',
  endpoints: {
    queryTypes: '/api/v1/query/types',
    queryRate: '/api/v1/query/rate',
    averageDuration: '/api/v1/query/average_duration',
    queryStatusDistribution: '/api/v1/query/status_distribution',
    queryLatencyTrends: '/api/v1/query/latency',
    queryThroughputAnalysis: '/api/v1/query/throughput',
    queryErrorAnalysis: '/api/v1/query/errors',
    recentQueries: '/api/v1/query/recent_queries',
  }
};

const DEFAULT_ERROR_VALUES = {
  queryTypes: {
    total_queries: 0,
    instant_percent: 0,
    range_percent: 0,
  },
  queryRate: {
    success_total: 0,
    error_total: 0,
    success_rate_percent: 0,
    error_rate_percent: 0,
  },
  averageDuration: {
    avg_duration: 0,
    delta_percent: 0,
  },
  recentQueries: {
    total: 0,
    totalPages: 0,
    data: [],
  },
};

function getUTCDate(date?: string): string {
  if (!date) {
    return new Date().toISOString();
  }
  return new Date(date).toISOString();
}

type ApiResponse = QueryTypesResponse | QueryRateResponse | AverageDurationResponse | QueryStatusDistributionResult[] | QueryLatencyTrendsResult[] | QueryThroughputAnalysisResult[] | PagedResult<RecentQuery>;

async function fetchApiData<T extends ApiResponse>(
  endpoint: string,
  options: FetchOptions = {}
): Promise<T> {
  const { from, to, page, pageSize, sortBy, sortOrder, filter, metricName } = options;
  const fromUTC = getUTCDate(from);
  const toUTC = getUTCDate(to);

  const queryParams = new URLSearchParams({
    from: fromUTC,
    to: toUTC,
    ...(page !== undefined && { page: page.toString() }),
    ...(pageSize !== undefined && { pageSize: pageSize.toString() }),
    ...(sortBy && { sortBy }),
    ...(sortOrder && { sortOrder }),
    ...(filter && { filter }),
    ...(metricName && { metricName }),
  });

  try {
    const response = await fetch(`${API_CONFIG.baseUrl}${endpoint}?${queryParams}`);
    if (!response.ok) {
      throw new Error(`HTTP error! status: ${response.status}`);
    }
    return await response.json() as T;
  } catch (error) {
    console.error(`Failed to fetch data from ${endpoint}:`, error);
    throw error;
  }
}

function withErrorHandling<T extends ApiResponse>(
  fn: () => Promise<T>,
  fallback: T
): Promise<T> {
  try {
    return fn();
  } catch {
    return Promise.resolve(fallback);
  }
}

export async function getQueryTypes(from?: string, to?: string): Promise<QueryTypesResponse> {
  return withErrorHandling(
    () => fetchApiData<QueryTypesResponse>(API_CONFIG.endpoints.queryTypes, { from, to }),
    DEFAULT_ERROR_VALUES.queryTypes
  );
}

export async function getQueryRate(from?: string, to?: string): Promise<QueryRateResponse> {
  return withErrorHandling(
    () => fetchApiData<QueryRateResponse>(API_CONFIG.endpoints.queryRate, { from, to }),
    DEFAULT_ERROR_VALUES.queryRate
  );
}

export async function getAverageDuration(from?: string, to?: string): Promise<AverageDurationResponse> {
  return withErrorHandling(
    () => fetchApiData<AverageDurationResponse>(API_CONFIG.endpoints.averageDuration, { from, to }),
    DEFAULT_ERROR_VALUES.averageDuration
  );
}

export async function getQueryStatusDistribution(from?: string, to?: string): Promise<QueryStatusDistributionResult[]> {
  return withErrorHandling(
    () => fetchApiData<QueryStatusDistributionResult[]>(API_CONFIG.endpoints.queryStatusDistribution, { from, to }),
    []
  );
}

export async function getQueryLatencyTrends(from?: string, to?: string, metricName?: string): Promise<QueryLatencyTrendsResult[]> {
  return withErrorHandling(
    () => fetchApiData<QueryLatencyTrendsResult[]>(API_CONFIG.endpoints.queryLatencyTrends, { from, to, metricName }),
    []
  );
}

export async function getQueryThroughputAnalysis(from?: string, to?: string): Promise<QueryThroughputAnalysisResult[]> {
  return withErrorHandling(
    () => fetchApiData<QueryThroughputAnalysisResult[]>(API_CONFIG.endpoints.queryThroughputAnalysis, { from, to }),
    []
  );
}

export async function getQueryErrorAnalysis(from?: string, to?: string): Promise<QueryErrorAnalysisResult[]> {
  return withErrorHandling(
    () => fetchApiData<QueryErrorAnalysisResult[]>(API_CONFIG.endpoints.queryErrorAnalysis, { from, to }),
    []
  );
}

export async function getRecentQueries(
  from?: string,
  to?: string,
  page?: number,
  pageSize?: number,
  sortBy?: string,
  sortOrder?: string,
  filter?: string
): Promise<PagedResult<RecentQuery>> {
  return withErrorHandling(
    () => fetchApiData<PagedResult<RecentQuery>>(API_CONFIG.endpoints.recentQueries, {
      from,
      to,
      page,
      pageSize,
      sortBy,
      sortOrder,
      filter,
    }),
    DEFAULT_ERROR_VALUES.recentQueries
  );
}
