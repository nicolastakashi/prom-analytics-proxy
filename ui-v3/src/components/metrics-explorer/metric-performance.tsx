"use client"

import { Card, CardContent, CardHeader, CardTitle } from "@/components/ui/card"
import { Database, CheckCircle, XCircle, BarChart2, LineChart, Clock } from "lucide-react"
import { MetricQueryPerformanceStatistics, QueryLatencyTrendsResult } from "@/lib/types"
import { formatDuration, formatUnit } from "@/lib/utils"
import { QueryLatencyTrends } from "@/components/query-latency-trends"
import { StatCard } from "@/components/ui/stat-card"

interface MetricPerformanceProps {
  queryPerformanceData: MetricQueryPerformanceStatistics | undefined
  queryLatencyTrendsData: QueryLatencyTrendsResult[] | undefined
}

export function MetricPerformance({ queryPerformanceData, queryLatencyTrendsData }: MetricPerformanceProps) {
  const from = new Date(queryLatencyTrendsData?.[0]?.time || new Date())
  const to = new Date(queryLatencyTrendsData?.[queryLatencyTrendsData.length - 1]?.time || new Date())

  const getSuccessRateColor = (rate: number) => {
    if (rate > 95) return "bg-green-500"
    if (rate > 80) return "bg-[hsl(var(--warning))]"
    return "bg-[hsl(var(--destructive))]"
  }

  const getErrorRateColor = (rate: number) => {
    if (rate > 20) return "bg-[hsl(var(--destructive))]"
    if (rate > 5) return "bg-[hsl(var(--warning))]"
    return "bg-[hsl(var(--destructive))]"
  }

  return (
    <Card>
      <CardHeader>
        <CardTitle>Performance Statistics</CardTitle>
      </CardHeader>
      <CardContent>
        <div className="space-y-8">
          <div className="grid grid-cols-1 md:grid-cols-3 gap-6">
            <StatCard
              title="Total Queries"
              value={queryPerformanceData?.totalQueries || 0}
              icon={Database}
              tooltipContent="Total number of queries executed using this metric in the selected time period"
            />
            <StatCard
              title="Success Rate"
              value={`${queryPerformanceData?.queryRate?.success_rate_percent || 0}%`}
              icon={CheckCircle}
              tooltipContent="Percentage of queries that succeeded."
              showStatusIndicator
              statusColor={getSuccessRateColor(queryPerformanceData?.queryRate?.success_rate_percent || 0)}
            />
            <StatCard
              title="Error Rate"
              value={`${queryPerformanceData?.queryRate?.error_rate_percent || 0}%`}
              icon={XCircle}
              tooltipContent="Percentage of queries that failed or returned errors."
              showStatusIndicator
              statusColor={getErrorRateColor(queryPerformanceData?.queryRate?.error_rate_percent || 0)}
            />
            <StatCard
              title="Average Samples"
              value={formatUnit(queryPerformanceData?.averageSamples || 0)}
              icon={BarChart2}
              tooltipContent="Average number of data points processed per query"
            />
            <StatCard
              title="Peak Samples"
              value={formatUnit(queryPerformanceData?.peakSamples || 0)}
              icon={LineChart}
              tooltipContent="Maximum number of data points processed in a single query"
            />
            <StatCard
              title="Average Duration"
              value={formatDuration(queryPerformanceData?.averageDuration || 0)}
              icon={Clock}
              tooltipContent="Average duration of queries in milliseconds"
            />
          </div>

          <div className="relative">
            <QueryLatencyTrends
              latencyTrendsData={queryLatencyTrendsData || []}
              from={from}
              to={to}
              title="Query Latency Trends"
            />
          </div>
        </div>
      </CardContent>
    </Card>
  )
}
