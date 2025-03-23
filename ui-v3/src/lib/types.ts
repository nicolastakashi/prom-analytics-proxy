export interface QueryTypesResponse {
	total_queries: number
	instant_percent: number
	range_percent: number
}

export interface AverageDurationResponse {
	avg_duration: number
	delta_percent: number
}

export interface QueryRateResponse {
	success_total: number
	error_total: number
	success_rate_percent: number
	error_rate_percent: number
}

export interface DateRange {
	from?: Date;
	to?: Date;
}
