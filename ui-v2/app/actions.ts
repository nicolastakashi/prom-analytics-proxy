'use server'

import { AverageDurationResponse, QueryRateResponse, QueryTypesResponse } from "@/lib/types"

const apiUrl = process.env.NEXT_PUBLIC_API_URL || 'http://localhost:9091'

function getUTCDate(date?: string) {
  if (!date) {
    return new Date().toISOString()
  }
  const dateObj = new Date(date)
  dateObj.setHours(0, 0, 0, 0)
  return dateObj.toISOString()
}


export async function getQueryTypes(from?: string, to?: string): Promise<QueryTypesResponse> {
  const fromUTC = getUTCDate(from)
  const toUTC = getUTCDate(to)
  try {
    const response = await fetch(`${apiUrl}/api/v1/query/types?from=${fromUTC}&to=${toUTC}`)
    const json = await response.json()
    console.log(json)
    return json as QueryTypesResponse
  } catch (error) {
    const msg = `Failed to fetch query types: ${error}`
    console.error(msg)
    return {
      total_queries: 0,
      instant_percent: 0,
      range_percent: 0,
    }
  }
}

export async function getQueryRate(from?: string, to?: string): Promise<QueryRateResponse> {
  const fromUTC = getUTCDate(from)
  const toUTC = getUTCDate(to)
  try {
    const response = await fetch(`${apiUrl}/api/v1/query/rate?from=${fromUTC}&to=${toUTC}`)
    const json = await response.json()
    return json as QueryRateResponse
  } catch (error) {
    const msg = `Failed to fetch query rate: ${error}`
    console.error(msg)
    return {  
      success_total: 0,
      error_total: 0,
      success_rate_percent: 0,
      error_rate_percent: 0,
    }
  }
}

export async function getAverageDuration(from?: string, to?: string): Promise<AverageDurationResponse> {
  const fromUTC = getUTCDate(from)
  const toUTC = getUTCDate(to)
  try {
    const response = await fetch(`${apiUrl}/api/v1/query/average_duration?from=${fromUTC}&to=${toUTC}`)
    const json = await response.json()
    console.log(json)
    return json as AverageDurationResponse
  } catch (error) {
    const msg = `Failed to fetch average duration: ${error}`
    console.error(msg)

    return {
      avg_duration: 0,
      delta_percent: 0,
    }
  }
}
