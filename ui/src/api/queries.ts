import {
  AverageDurationResponse,
  PagedResult,
  QueryErrorAnalysisResult,
  QueryLatencyTrendsResult,
  QueryRateResponse,
  QueryStatusDistributionResult,
  QueryThroughputAnalysisResult,
  QueryTypesResponse,
  QueryTimeRangeDistributionResult,
  QueryExpression,
  QueryExecution,
} from '@/lib/types';
import { ConfigResponse } from '@/types/config';
import { toUTC } from '@/lib/utils/date-utils';

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
    queryTimeRangeDistribution: string;
    queryExpressions: string;
    queryExecutions: string;
    configs: string;
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
  format?: 'json' | 'yaml';
  fingerprint?: string;
  type?: string;
}

const API_CONFIG: ApiConfig = {
  baseUrl:
    process.env.NODE_ENV === 'development' ? 'http://localhost:9091' : '',
  endpoints: {
    queryTypes: '/api/v1/query/types',
    queryRate: '/api/v1/query/rate',
    averageDuration: '/api/v1/query/average_duration',
    queryStatusDistribution: '/api/v1/query/status_distribution',
    queryLatencyTrends: '/api/v1/query/latency',
    queryThroughputAnalysis: '/api/v1/query/throughput',
    queryErrorAnalysis: '/api/v1/query/errors',
    queryTimeRangeDistribution: '/api/v1/query/time_range_distribution',
    queryExpressions: '/api/v1/query/expressions',
    queryExecutions: '/api/v1/query/executions',
    configs: '/api/v1/configs',
  },
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
  configs: {},
};

function getUTCDate(date?: string): string {
  if (!date) {
    return new Date().toISOString();
  }
  // Use our utility function to ensure date is in UTC
  return toUTC(date);
}

type EmptyObject = Record<string, never>;

type ApiResponse =
  | QueryTypesResponse
  | QueryRateResponse
  | AverageDurationResponse
  | QueryStatusDistributionResult[]
  | QueryLatencyTrendsResult[]
  | QueryThroughputAnalysisResult[]
  | QueryErrorAnalysisResult[]
  | QueryTimeRangeDistributionResult[]
  | PagedResult<QueryExpression>
  | PagedResult<QueryExecution>
  | ConfigResponse
  | string
  | EmptyObject;

async function fetchApiData<T extends ApiResponse>(
  endpoint: string,
  options: FetchOptions = {},
): Promise<T> {
  const {
    from,
    to,
    page,
    pageSize,
    sortBy,
    sortOrder,
    filter,
    metricName,
    format,
    fingerprint,
  } = options;
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
    ...(format && { format }),
    ...(fingerprint && { fingerprint }),
    ...(options.type && { type: options.type }),
  });

  try {
    const response = await fetch(
      `${API_CONFIG.baseUrl}${endpoint}?${queryParams}`,
    );
    if (!response.ok) {
      throw new Error(`HTTP error! status: ${response.status}`);
    }

    // If format is yaml, return the text directly
    if (format === 'yaml') {
      return (await response.text()) as T;
    }

    // Otherwise parse as JSON
    return (await response.json()) as T;
  } catch (error) {
    console.error(`Failed to fetch data from ${endpoint}:`, error);
    throw error;
  }
}

function withErrorHandling<T extends ApiResponse>(
  fn: () => Promise<T>,
  fallback: T,
): Promise<T> {
  try {
    return fn();
  } catch {
    return Promise.resolve(fallback);
  }
}

export async function getQueryTypes(
  from?: string,
  to?: string,
  fingerprint?: string,
): Promise<QueryTypesResponse> {
  return withErrorHandling(
    () =>
      fetchApiData<QueryTypesResponse>(API_CONFIG.endpoints.queryTypes, {
        from,
        to,
        fingerprint,
      }),
    DEFAULT_ERROR_VALUES.queryTypes,
  );
}

export async function getQueryRate(
  from?: string,
  to?: string,
  fingerprint?: string,
): Promise<QueryRateResponse> {
  return withErrorHandling(
    () =>
      fetchApiData<QueryRateResponse>(API_CONFIG.endpoints.queryRate, {
        from,
        to,
        fingerprint,
      }),
    DEFAULT_ERROR_VALUES.queryRate,
  );
}

export async function getAverageDuration(
  from?: string,
  to?: string,
  fingerprint?: string,
): Promise<AverageDurationResponse> {
  return withErrorHandling(
    () =>
      fetchApiData<AverageDurationResponse>(
        API_CONFIG.endpoints.averageDuration,
        { from, to, fingerprint },
      ),
    DEFAULT_ERROR_VALUES.averageDuration,
  );
}

export async function getQueryStatusDistribution(
  from?: string,
  to?: string,
  fingerprint?: string,
): Promise<QueryStatusDistributionResult[]> {
  return withErrorHandling(
    () =>
      fetchApiData<QueryStatusDistributionResult[]>(
        API_CONFIG.endpoints.queryStatusDistribution,
        { from, to, fingerprint },
      ),
    [],
  );
}

export async function getQueryLatencyTrends(
  from?: string,
  to?: string,
  metricName?: string,
  fingerprint?: string,
): Promise<QueryLatencyTrendsResult[]> {
  return withErrorHandling(
    () =>
      fetchApiData<QueryLatencyTrendsResult[]>(
        API_CONFIG.endpoints.queryLatencyTrends,
        { from, to, metricName, fingerprint },
      ),
    [],
  );
}

export async function getQueryThroughputAnalysis(
  from?: string,
  to?: string,
): Promise<QueryThroughputAnalysisResult[]> {
  return withErrorHandling(
    () =>
      fetchApiData<QueryThroughputAnalysisResult[]>(
        API_CONFIG.endpoints.queryThroughputAnalysis,
        { from, to },
      ),
    [],
  );
}

export async function getQueryErrorAnalysis(
  from?: string,
  to?: string,
  fingerprint?: string,
): Promise<QueryErrorAnalysisResult[]> {
  return withErrorHandling(
    () =>
      fetchApiData<QueryErrorAnalysisResult[]>(
        API_CONFIG.endpoints.queryErrorAnalysis,
        { from, to, fingerprint },
      ),
    [],
  );
}

export async function getQueryTimeRangeDistribution(
  from?: string,
  to?: string,
  fingerprint?: string,
): Promise<QueryTimeRangeDistributionResult[]> {
  return withErrorHandling(
    () =>
      fetchApiData<QueryTimeRangeDistributionResult[]>(
        API_CONFIG.endpoints.queryTimeRangeDistribution,
        { from, to, fingerprint },
      ),
    [],
  );
}

export async function getConfigurations(
  format: 'json' | 'yaml' = 'json',
): Promise<ConfigResponse | EmptyObject> {
  return withErrorHandling(
    () =>
      fetchApiData<ConfigResponse>(API_CONFIG.endpoints.configs, { format }),
    DEFAULT_ERROR_VALUES.configs,
  );
}

export async function getQueryExpressions(
  from?: string,
  to?: string,
  page?: number,
  pageSize?: number,
  sortBy?: string,
  sortOrder?: string,
  filter?: string,
): Promise<PagedResult<QueryExpression>> {
  return withErrorHandling(
    () =>
      fetchApiData<PagedResult<QueryExpression>>(
        API_CONFIG.endpoints.queryExpressions,
        {
          from,
          to,
          page,
          pageSize,
          sortBy,
          sortOrder,
          filter,
        },
      ),
    { total: 0, totalPages: 0, data: [] },
  );
}

export async function getQueryExecutions(
  fingerprint: string,
  from?: string,
  to?: string,
  page?: number,
  pageSize?: number,
  sortBy?: string,
  sortOrder?: string,
  type?: 'all' | 'instant' | 'range',
): Promise<PagedResult<QueryExecution>> {
  return withErrorHandling(
    () =>
      fetchApiData<PagedResult<QueryExecution>>(
        API_CONFIG.endpoints.queryExecutions,
        {
          fingerprint,
          from,
          to,
          page,
          pageSize,
          sortBy,
          sortOrder,
          type,
        },
      ),
    { total: 0, totalPages: 0, data: [] },
  );
}
