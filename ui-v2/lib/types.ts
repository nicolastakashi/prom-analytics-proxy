export interface QueryTypesResponse {
	total_queries: number
	instant_percent: number
	range_percent: number
}

export interface AverageDurationResponse {
	avg_duration: number
	delta_percent: number
}
