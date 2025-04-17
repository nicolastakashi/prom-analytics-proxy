import { MetricQueryPerformanceStatistics, MetricStatistics, PagedResult } from "@/lib/types";
import { MetricMetadata } from "@/lib/types";

interface SerieExpression {
  query: string;
  avgDuration: number;
  maxPeakSamples: number;
  avgPeakySamples: number;
  ts: string;
}

interface SerieExpressionsResponse {
  data: SerieExpression[];
  total: number;
  totalPages: number;
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

interface ApiConfig {
  baseUrl: string;
  endpoints: {
    seriesMetadata: string;
    metricStatistics: string;
    metricQueryPerformanceStatistics: string;
    serieExpressions: string;
    metricUsage: string;
  };
}

interface FetchOptions {
  page?: number;
  pageSize?: number;
  sortBy?: string;
  sortOrder?: string;
  filter?: string;
  type?: string;
  from?: string;
  to?: string;
  kind?: string;
}

const API_CONFIG: ApiConfig = {
  baseUrl: 'http://localhost:9091',
  endpoints: {
    seriesMetadata: '/api/v1/seriesMetadata',
    metricStatistics: '/api/v1/metricStatistics', 
    metricQueryPerformanceStatistics: '/api/v1/metricQueryPerformanceStatistics',
    serieExpressions: '/api/v1/serieExpressions',
    metricUsage: '/api/v1/serieUsage',
  }
};

const DEFAULT_ERROR_VALUES = {
  seriesMetadata: {
    total: 0,
    totalPages: 0,
    data: [],
  },
  metricStatistics: {
    serieCount: 0,
    labelCount: 0,
    alertCount: 0,
    recordCount: 0,
    dashboardCount: 0,
    totalAlerts: 0,
    totalRecords: 0,
    totalDashboards: 0,
  },
  metricQueryPerformanceStatistics: {
    queryRate: {
      success_total: 0,
      error_total: 0,
      success_rate_percent: 0,
      error_rate_percent: 0,
    },
    totalQueries: 0,
    averageSamples: 0,
    peakSamples: 0,
    averageDuration: 0,
  },
  serieExpressions: {
    data: [],
    total: 0,
    totalPages: 0,
  },
  metricUsage: {
    data: [],
    total: 0,
    totalPages: 0,
  },
};

async function fetchApiData<T>(
  endpoint: string,
  options: FetchOptions = {}
): Promise<T> {
  const { page, pageSize, sortBy, sortOrder, filter, type, from, to, kind } = options;

  const queryParams = new URLSearchParams({
    ...(page !== undefined && { page: page.toString() }),
    ...(pageSize !== undefined && { pageSize: pageSize.toString() }),
    ...(sortBy && { sortBy }),
    ...(sortOrder && { sortOrder }),
    ...(filter && { filter }),
    ...(type && { type }),
    ...(from && { from }),
    ...(to && { to }),
    ...(kind && { kind }),
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

function withErrorHandling<T>(
  fn: () => Promise<T>,
  fallback: T
): Promise<T> {
  try {
    return fn();
  } catch {
    return Promise.resolve(fallback);
  }
}

export async function getSeriesMetadata(
  page: number = 1,
  pageSize: number = 10,
  sortBy: string = "name",
  sortOrder: string = "asc",
  filter: string = "",
  type: string = ""
): Promise<PagedResult<MetricMetadata>> {
  return withErrorHandling(
    () => fetchApiData<PagedResult<MetricMetadata>>(
      API_CONFIG.endpoints.seriesMetadata,
      { page, pageSize, sortBy, sortOrder, filter, type }
    ),
    DEFAULT_ERROR_VALUES.seriesMetadata
  );
}

export async function getMetricStatistics(
  metricName: string,
  from: string,
  to: string
): Promise<MetricStatistics> {
  return withErrorHandling(
    () => fetchApiData<MetricStatistics>(
      API_CONFIG.endpoints.metricStatistics + `/${metricName}`,
      { from, to }
    ),
    DEFAULT_ERROR_VALUES.metricStatistics
  );
}

export async function getMetricQueryPerformanceStatistics(
  metricName: string,
  from: string,
  to: string
): Promise<MetricQueryPerformanceStatistics> {
  return withErrorHandling(
    () => fetchApiData<MetricQueryPerformanceStatistics>(
      API_CONFIG.endpoints.metricQueryPerformanceStatistics + `/${metricName}`,
      { from, to }
    ),
    DEFAULT_ERROR_VALUES.metricQueryPerformanceStatistics
  );
}

export async function getSerieExpressions(
  metricName: string,
  page: number = 1,
  pageSize: number = 10,
  from: string = "",
  to: string = ""
): Promise<SerieExpressionsResponse> {
  const url = API_CONFIG.endpoints.serieExpressions + `/${metricName}`;
  const params: Record<string, string | number> = { page, pageSize };
  
  if (from) {
    params.from = from;
  }
  if (to) {
    params.to = to;
  }
  
  return withErrorHandling(
    () => fetchApiData<SerieExpressionsResponse>(url, params),
    DEFAULT_ERROR_VALUES.serieExpressions
  );
}

export async function getMetricUsage(
  metricName: string,
  kind: string,
  page: number = 1,
  pageSize: number = 10,
  from: string = "",
  to: string = ""
): Promise<MetricUsageResponse> {
  const url = API_CONFIG.endpoints.metricUsage + `/${metricName}`;
  const params: Record<string, string | number> = { kind, page, pageSize };
  
  if (from) {
    params.from = from;
  }
  if (to) {
    params.to = to;
  }
  
  return withErrorHandling(
    () => fetchApiData<MetricUsageResponse>(url, params),
    DEFAULT_ERROR_VALUES.metricUsage
  );
}