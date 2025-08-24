"use client"
import { Card, CardContent, CardHeader, CardTitle } from "@/components/ui/card"
import { QueryLatencyTrendsResult } from "@/lib/types"
import { formatTimestampByGranularity } from "@/lib/utils/date-formatting"
import { Area, AreaChart, ResponsiveContainer, Tooltip, XAxis, YAxis, CartesianGrid } from "recharts"

import { useQuery } from "@tanstack/react-query"
import { useDateRange } from "@/contexts/date-range-context"
import { getQueryLatencyTrends } from "@/api/queries"
import { Skeleton } from "@/components/ui/skeleton"

interface QueryLatencyTrendsProps {
  className?: string
  title?: React.ReactNode
  metricName?: string
  fingerprint?: string
}

export function QueryLatencyTrends({ className, title, metricName, fingerprint }: QueryLatencyTrendsProps) {
  const { dateRange } = useDateRange()
  const fromISO = dateRange?.from?.toISOString()
  const toISO = dateRange?.to?.toISOString()
  const fromDate = dateRange?.from ?? new Date()
  const toDate = dateRange?.to ?? new Date()

  const { data: latencyTrendsData, isLoading } = useQuery<QueryLatencyTrendsResult[]>({
    queryKey: ["queryLatencyTrends", fromISO, toISO, metricName, fingerprint],
    queryFn: () => getQueryLatencyTrends(fromISO, toISO, metricName, fingerprint),
    enabled: Boolean(fromISO && toISO),
  })

  if (isLoading || !latencyTrendsData) {
    return (
      <Card className={className}>
        <CardHeader>
          <CardTitle>
            <Skeleton className="h-4 w-32" />
          </CardTitle>
        </CardHeader>
        <CardContent>
          <Skeleton className="h-[300px] w-full" />
        </CardContent>
      </Card>
    );
  }
  return (
    <Card className={className}>
      <CardHeader>
        <CardTitle>{title || "Query Latency Trends"}</CardTitle>
      </CardHeader>
      <CardContent>
        <div className="h-[300px] w-full">
          <ResponsiveContainer width="100%" height="100%">
            <AreaChart data={latencyTrendsData} margin={{ top: 0, right: 16, left: 0, bottom: 0 }}>
              <CartesianGrid strokeDasharray="3 3" stroke="#888888" opacity={0.2} />
              <XAxis
                dataKey="time"
                stroke="#888888"
                fontSize={12}
                tickLine={false}
                axisLine={false}
                angle={-45}
                textAnchor="end"
                height={60}
                tickFormatter={(value) => formatTimestampByGranularity(value, fromDate, toDate)}
              />
              <YAxis
                stroke="#888888"
                fontSize={12}
                tickLine={false}
                axisLine={false}
                tickFormatter={(value) => `${value}ms`}
              />
              <Tooltip
                content={({ active, payload }) => {
                  if (active && payload && payload.length) {
                    return (
                      <div className="rounded-lg border bg-background p-2 shadow-sm">
                        <div className="grid gap-2">
                          <div className="flex flex-col">
                            <span className="text-[0.70rem] uppercase text-muted-foreground">Time</span>
                            <span className="font-bold text-muted-foreground">{payload[0].payload.time}</span>
                          </div>
                          <div className="flex flex-col">
                            <span className="text-[0.70rem] uppercase text-muted-foreground">Avg Latency</span>
                            <span className="font-bold">{payload[0].value}ms</span>
                          </div>
                          <div className="flex flex-col">
                            <span className="text-[0.70rem] uppercase text-muted-foreground">p95 Latency</span>
                            <span className="font-bold">{payload[1].value}ms</span>
                          </div>
                        </div>
                      </div>
                    )
                  }
                  return null
                }}
              />
              <Area
                type="monotone"
                dataKey="value"
                stroke="hsl(var(--primary))"
                fill="hsl(var(--primary))"
                fillOpacity={0.2}
                strokeWidth={2}
              />
              <Area
                type="monotone"
                dataKey="p95"
                stroke="hsl(var(--warning))"
                fill="hsl(var(--warning))"
                fillOpacity={0.1}
                strokeWidth={2}
                strokeDasharray="4 4"
              />
            </AreaChart>
          </ResponsiveContainer>
        </div>
        <div className="mt-4 flex items-center justify-center gap-4">
          <div className="flex items-center gap-2">
            <div className="h-3 w-3 rounded-full bg-[hsl(var(--primary)/.3)]" />
            <span className="text-sm">Average</span>
          </div>
          <div className="flex items-center gap-2">
            <div className="h-3 w-3 rounded-full bg-[hsl(var(--warning)/.3)]" />
            <span className="text-sm">95th Percentile</span>
          </div>
        </div>
      </CardContent>
    </Card>
  )
}

