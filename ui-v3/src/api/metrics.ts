import { MetricQueryPerformanceStatistics, MetricStatistics, PagedResult } from "@/lib/types";
import { MetricMetadata } from "@/lib/types";

interface ApiConfig {
  baseUrl: string;
    endpoints: {
    seriesMetadata: string;
    metricStatistics: string;
    metricQueryPerformanceStatistics: string;
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
}

const API_CONFIG: ApiConfig = {
  baseUrl: 'http://localhost:9091',
  endpoints: {
    seriesMetadata: '/api/v1/seriesMetadata',
    metricStatistics: '/api/v1/metricStatistics', 
    metricQueryPerformanceStatistics: '/api/v1/metricQueryPerformanceStatistics',
  }
};

const DEFAULT_ERROR_VALUES = {
  seriesMetadata: {
    total: 0,
    totalPages: 0,
    data: [],
  },
  metricStatistics: {
    serie_count: 0,
    label_count: 0,
    alert_count: 0,
    record_count: 0,
    dashboard_count: 0,
    total_alerts: 0,
    total_records: 0,
    total_dashboards: 0,
  },
  metricQueryPerformanceStatistics: {
    queryRate: 0,
  },
};

async function fetchApiData<T>(
  endpoint: string,
  options: FetchOptions = {}
): Promise<T> {
  const { page, pageSize, sortBy, sortOrder, filter, type, from, to } = options;

  const queryParams = new URLSearchParams({
    ...(page !== undefined && { page: page.toString() }),
    ...(pageSize !== undefined && { pageSize: pageSize.toString() }),
    ...(sortBy && { sortBy }),
    ...(sortOrder && { sortOrder }),
    ...(filter && { filter }),
    ...(type && { type }),
    ...(from && { from }),
    ...(to && { to }),
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