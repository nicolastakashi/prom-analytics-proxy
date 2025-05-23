"use client"

import { Card, CardContent, CardHeader, CardTitle } from "@/components/ui/card"
import { AverageDurationResponse, QueryRateResponse, QueryTypesResponse } from "@/lib/types"
import { formatDuration } from "@/lib/utils"
import { Activity, AlertTriangle, Clock, Filter } from "lucide-react"
import { PieChart, Pie, ResponsiveContainer, Cell, Tooltip } from "recharts"

const COLORS = ["hsl(var(--primary))", "hsl(var(--primary) / 0.3)"]

interface KeyMetricsProps {
    queryTypes?: QueryTypesResponse
    averageDuration?: AverageDurationResponse
    queryRate?: QueryRateResponse
}

export function KeyMetrics(props: KeyMetricsProps) {
    const { queryTypes, averageDuration, queryRate } = props

    let queryTypeData: { name: string, value: number }[] = []
    if (queryTypes) {
        queryTypeData = [
            { name: "Instant", value: queryTypes.instant_percent },
            { name: "Range", value: queryTypes.range_percent },
        ]
    }

    return (
      <div className="grid gap-4 md:grid-cols-2 lg:grid-cols-5">
        <Card className="lg:col-span-2 gap-2">
          <CardHeader className="flex flex-row items-center justify-between space-y-0 pb-1 pt-3">
            <CardTitle className="text-sm font-medium">Query Types</CardTitle>
            <Filter className="h-4 w-4 text-muted-foreground" />
          </CardHeader>
          <CardContent className="pb-3">
            <div className="flex items-center gap-3">
              <div className="h-[70px] w-[70px]">
                <ResponsiveContainer width="100%" height="100%">
                  <PieChart>
                    <Pie data={queryTypeData} innerRadius={25} outerRadius={35} paddingAngle={2} dataKey="value">
                      {queryTypeData.map((_, index) => (
                        <Cell key={`cell-${index}`} fill={COLORS[index]} />
                      ))}
                    </Pie>
                    <Tooltip
                      content={({ active, payload }) => {
                        if (active && payload && payload.length) {
                          return (
                            <div className="rounded-lg border bg-background p-2 shadow-sm">
                              <div className="grid grid-cols-2 gap-2">
                                <div className="flex flex-col">
                                  <span className="text-[0.70rem] uppercase text-muted-foreground">
                                    {payload[0].name}
                                  </span>
                                  <span className="font-bold">{parseFloat(payload[0].value as string).toFixed(2)}%</span>
                                </div>
                              </div>
                            </div>
                          )
                        }
                        return null
                      }}
                    />
                  </PieChart>
                </ResponsiveContainer>
              </div>
              <div className="space-y-0.5">
                <p className="text-xl font-bold">{queryTypes?.total_queries || 0}</p>
                <p className="text-xs text-muted-foreground">Total queries in selected period</p>
                <div className="mt-2 flex items-center gap-2 text-xs">
                  <div className="flex items-center gap-1">
                    <div className="h-2 w-2 rounded-full bg-chart-1" />
                    Instant ({queryTypeData[0]?.value ? parseFloat(queryTypeData[0].value.toString()).toFixed(2) : 0}%)
                  </div>
                  <div className="flex items-center gap-1">
                    <div className="h-2 w-2 rounded-full bg-chart-2" />
                    Range ({queryTypeData[1]?.value ? parseFloat(queryTypeData[1].value.toString()).toFixed(2) : 0}%)
                  </div>
                </div>
              </div>
            </div>
          </CardContent>
        </Card>
  
        <Card className="gap-2">
          <CardHeader className="flex flex-row items-center justify-between space-y-0 pb-1 pt-3">
            <CardTitle className="text-sm font-medium">Avg Duration</CardTitle>
            <Clock className="h-4 w-4 text-muted-foreground" />
          </CardHeader>
          <CardContent className="pb-3">
            <div className="space-y-0.5">
              <p className="text-2xl font-bold">{formatDuration(averageDuration?.avg_duration || 0)}</p>
              <p className="text-xs text-muted-foreground">
                {averageDuration?.delta_percent != null 
                  ? `${averageDuration.delta_percent.toFixed(2)}% from previous period`
                  : 'No previous data'
                }
              </p>
            </div>
          </CardContent>
        </Card>
  
        <Card className="gap-2">
          <CardHeader className="flex flex-row items-center justify-between space-y-0 pb-1 pt-3">
            <CardTitle className="text-sm font-medium">Success Rate</CardTitle>
            <Activity className="h-4 w-4 text-muted-foreground" />
          </CardHeader>
          <CardContent className="pb-3">
            <div className="space-y-0.5">
              <p className="text-2xl font-bold">
                {queryRate?.success_rate_percent != null 
                  ? `${queryRate.success_rate_percent.toFixed(2)}%` 
                  : '0%'
                }
              </p>
              <div className="flex items-center gap-1 text-xs">
                <div className="h-2 w-2 rounded-full bg-green-500" />
                <span className="text-muted-foreground">{queryRate?.success_total || 0} successful</span>
              </div>
            </div>
          </CardContent>
        </Card>
  
        <Card className="gap-2">
          <CardHeader className="flex flex-row items-center justify-between space-y-0 pb-1 pt-3">
            <CardTitle className="text-sm font-medium">Error Rate</CardTitle>
            <AlertTriangle className="h-4 w-4 text-muted-foreground" />
          </CardHeader>
          <CardContent className="pb-3">
            <div className="space-y-0.5">
              <p className="text-2xl font-bold">
                {queryRate?.error_rate_percent != null 
                  ? `${queryRate.error_rate_percent.toFixed(2)}%` 
                  : '0%'
                }
              </p>
              <div className="flex items-center gap-1 text-xs">
                <div className="h-2 w-2 rounded-full bg-red-500" />
                <span className="text-muted-foreground">{queryRate?.error_total || 0} failed</span>
              </div>
            </div>
          </CardContent>
        </Card>
      </div>
    )
  }
  