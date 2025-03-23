import { AverageDurationResponse, QueryRateResponse, QueryTypesResponse } from "@/lib/types"

const API_CONFIG = {
  baseUrl: 'http://localhost:9091',
  endpoints: {
    queryTypes: '/api/v1/query/types',
    queryRate: '/api/v1/query/rate',
    averageDuration: '/api/v1/query/average_duration'
  }
} as const

function getUTCDate(date?: string): string {
  if (!date) {
    return new Date().toISOString()
  }
  const dateObj = new Date(date)
  dateObj.setHours(0, 0, 0, 0)
  return dateObj.toISOString()
}

type ApiResponse = QueryTypesResponse | QueryRateResponse | AverageDurationResponse

async function fetchApiData<T extends ApiResponse>(endpoint: string, from?: string, to?: string): Promise<T> {
  const fromUTC = getUTCDate(from)
  const toUTC = getUTCDate(to)
  
  try {
    const response = await fetch(`${API_CONFIG.baseUrl}${endpoint}?from=${fromUTC}&to=${toUTC}`)
    const json = await response.json()
    return json as T
  } catch (error) {
    console.error(`Failed to fetch data from ${endpoint}: ${error}`)
    throw error
  }
}

export async function getQueryTypes(from?: string, to?: string): Promise<QueryTypesResponse> {
  try {
    return await fetchApiData<QueryTypesResponse>(API_CONFIG.endpoints.queryTypes, from, to)
  } catch {
    return {
      total_queries: 0,
      instant_percent: 0,
      range_percent: 0,
    }
  }
}

export async function getQueryRate(from?: string, to?: string): Promise<QueryRateResponse> {
  try {
    return await fetchApiData<QueryRateResponse>(API_CONFIG.endpoints.queryRate, from, to)
  } catch {
    return {
      success_total: 0,
      error_total: 0,
      success_rate_percent: 0,
      error_rate_percent: 0,
    }
  }
}

export async function getAverageDuration(from?: string, to?: string): Promise<AverageDurationResponse> {
  try {
    return await fetchApiData<AverageDurationResponse>(API_CONFIG.endpoints.averageDuration, from, to)
  } catch {
    return {
      avg_duration: 0,
      delta_percent: 0,
    }
  }
}