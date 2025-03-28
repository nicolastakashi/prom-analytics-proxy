import { format } from "date-fns"

type TimeGranularity = "15m" | "30m" | "1h" | "1d"

export function formatTimestampByGranularity(timestamp: string | number | Date, granularity: TimeGranularity) {
    const date = new Date(timestamp)
    
    switch (granularity) {
        case "15m":
        case "30m":
            // For 15 and 30 minute intervals, show hour:minute
            return format(date, "HH:mm")
        case "1h":
            // For hourly intervals, show hour
            return format(date, "HH:00")
        case "1d":
            // For daily intervals, show month/day
            return format(date, "MM/dd")
        default:
            return format(date, "HH:mm")
    }
} 