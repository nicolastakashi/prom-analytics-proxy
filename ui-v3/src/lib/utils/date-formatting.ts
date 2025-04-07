import { format } from "date-fns"

export function formatTimestampByGranularity(timestamp: string | number | Date, from: Date, to: Date) {
    const date = new Date(timestamp)
    const timeRange = to.getTime() - from.getTime()
    const hourInMs = 60 * 60 * 1000
    const dayInMs = 24 * hourInMs

    switch (true) {
        case timeRange <= hourInMs:
            // For ranges under 1 hour, show hour:minute
            return format(date, "HH:mm")
        case timeRange <= 6 * hourInMs:
            // For ranges 1-6 hours, show 15 min intervals
            return format(date, "HH:mm")
        case timeRange <= 24 * hourInMs:
            // For ranges 6-24 hours, show 30 min intervals
            return format(date, "HH:mm")
        case timeRange <= 7 * dayInMs:
            // For ranges 1-7 days, show hourly
            return format(date, "HH:00")
        default:
            // For ranges over 7 days, show daily
            return format(date, "MM/dd")
    }
}