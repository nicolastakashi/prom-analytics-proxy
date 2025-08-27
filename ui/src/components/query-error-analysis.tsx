"use client"
import { Card, CardContent, CardHeader, CardTitle } from "@/components/ui/card"
import { QueryErrorAnalysisResult } from "@/lib/types"
import { formatTimestampByGranularity } from "@/lib/utils/date-formatting"
import { Area, AreaChart, ResponsiveContainer, Tooltip, XAxis, YAxis, CartesianGrid } from "recharts"


import { useQuery } from "@tanstack/react-query"
import { useDateRange } from "@/contexts/date-range-context"
import { getQueryErrorAnalysis } from "@/api/queries"
import { Skeleton } from "@/components/ui/skeleton"

interface QueryErrorAnalysisProps {
  fingerprint?: string
}

export function QueryErrorAnalysis({ fingerprint }: QueryErrorAnalysisProps) {
  const { dateRange } = useDateRange()
  const fromISO = dateRange?.from?.toISOString()
  const toISO = dateRange?.to?.toISOString()
  const fromDate = dateRange?.from ?? new Date()
  const toDate = dateRange?.to ?? new Date()

  const { data, isLoading } = useQuery<QueryErrorAnalysisResult[]>({
    queryKey: ["queryErrorAnalysis", fromISO, toISO, fingerprint],
    queryFn: () => getQueryErrorAnalysis(fromISO, toISO, fingerprint),
    enabled: Boolean(fromISO && toISO),
  })

  if (isLoading || !data) {
    return (
      <Card>
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
    <Card>
      <CardHeader>
        <CardTitle>Query Error Analysis</CardTitle>
      </CardHeader>
      <CardContent>
        <div className="h-[300px] w-full">
          <ResponsiveContainer width="100%" height="100%">
            <AreaChart data={data} margin={{ top: 0, right: 0, bottom: 0, left: 0 }}>
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
                tickFormatter={(value) => `${value}/min`}
              />
              <Tooltip
                content={({ active, payload }) => {
                  if (active && payload && payload.length) {
                    return (
                      <div className="rounded-lg border bg-background p-2 shadow-sm">
                        <div className="grid gap-2">
                          <div className="flex flex-col">
                            <span className="text-[0.70rem] uppercase text-muted-foreground">Time</span>
                            <span className="font-bold text-muted-foreground">{formatTimestampByGranularity(payload[0].payload.time, fromDate, toDate)}</span>
                          </div>
                          <div className="flex flex-col">
                            <span className="text-[0.70rem] uppercase text-muted-foreground">Error Count</span>
                            <span className="font-bold">{payload[0].value}</span>
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
                stroke="hsl(var(--destructive))"
                fill="hsl(var(--destructive))"
                fillOpacity={0.2}
                strokeWidth={2}
              />
            </AreaChart>
          </ResponsiveContainer>
        </div>
      </CardContent>
    </Card>
  )
}

