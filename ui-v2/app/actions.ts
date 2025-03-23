'use server'

import { QueryTypesResponse } from "@/lib/types"

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
