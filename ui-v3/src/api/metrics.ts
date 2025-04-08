import { PagedResult } from "@/lib/types";
import { MetricMetadata } from "@/lib/types";

interface ApiConfig {
  baseUrl: string;
  endpoints: {
    seriesMetadata: string;
  };
}

interface FetchOptions {
  page?: number;
  pageSize?: number;
  sortBy?: string;
  sortOrder?: string;
  filter?: string;
  type?: string;
}

const API_CONFIG: ApiConfig = {
  baseUrl: 'http://localhost:9091',
  endpoints: {
    seriesMetadata: '/api/v1/seriesMetadata',
  }
};

const DEFAULT_ERROR_VALUES = {
  seriesMetadata: {
    total: 0,
    totalPages: 0,
    data: [],
  },
};

async function fetchApiData<T>(
  endpoint: string,
  options: FetchOptions = {}
): Promise<T> {
  const { page, pageSize, sortBy, sortOrder, filter, type } = options;

  const queryParams = new URLSearchParams({
    ...(page !== undefined && { page: page.toString() }),
    ...(pageSize !== undefined && { pageSize: pageSize.toString() }),
    ...(sortBy && { sortBy }),
    ...(sortOrder && { sortOrder }),
    ...(filter && { filter }),
    ...(type && { type }),
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