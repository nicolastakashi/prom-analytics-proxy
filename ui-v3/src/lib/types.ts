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

export interface QueryStatusDistributionResult {
	hour: string;
	status2xx: number;
	status4xx: number;
	status5xx: number;
}

export interface QueryLatencyTrendsResult {
	time: string;
	value: number;
	p95: number;
}

export interface QueryThroughputAnalysisResult {
	time: string;
	value: number;
}

export interface QueryErrorAnalysisResult {
	time: string;
	value: number;
}

export type TimeGranularity = "15m" | "30m" | "1h" | "1d"

export interface TimeRange {
    from: Date
    to: Date
    label: string
} 