"use client"

import { Card, CardContent, CardHeader, CardTitle } from "@/components/ui/card"
import { AverageDurationResponse, QueryRateResponse, QueryTypesResponse } from "@/lib/types"
import { formatDuration } from "@/lib/utils"
import { Activity, AlertTriangle, Clock, Filter } from "lucide-react"
import { PieChart, Pie, ResponsiveContainer, Cell, Tooltip } from "recharts"

const COLORS = ["hsl(var(--primary))", "hsl(var(--primary) / 0.3)"]

import { useQuery } from "@tanstack/react-query"
import { useDateRange } from "@/contexts/date-range-context"
import { getQueryTypes, getAverageDuration, getQueryRate } from "@/api/queries"
import { Skeleton } from "@/components/ui/skeleton"


export function KeyMetrics() {
    const { dateRange } = useDateRange()
    const from = dateRange?.from?.toISOString()
    const to = dateRange?.to?.toISOString()

    const {
      data: queryTypes,
      isLoading: isLoadingTypes,
    } = useQuery<QueryTypesResponse>({
      queryKey: ["queryTypes", from, to],
      queryFn: () => getQueryTypes(from, to),
      enabled: Boolean(from && to),
    })

    const {
      data: averageDuration,
      isLoading: isLoadingAvg,
    } = useQuery<AverageDurationResponse>({
      queryKey: ["averageDuration", from, to],
      queryFn: () => getAverageDuration(from, to),
      enabled: Boolean(from && to),
    })

    const {
      data: queryRate,
      isLoading: isLoadingRate,
    } = useQuery<QueryRateResponse>({
      queryKey: ["queryRate", from, to],
      queryFn: () => getQueryRate(from, to),
      enabled: Boolean(from && to),
    })

    const loading = isLoadingTypes || isLoadingAvg || isLoadingRate

    if (loading || !queryTypes || !averageDuration || !queryRate) {
      return (
        <div className="grid gap-4 md:grid-cols-2 lg:grid-cols-5">
                    {/* Large card placeholder */}
          <Card className="lg:col-span-2 relative">
            {/* title & icon */}
            <CardHeader className="flex flex-row items-center justify-between space-y-0 pb-1 pt-3">
              <CardTitle className="text-sm font-medium">
                <Skeleton className="h-4 w-24" />
              </CardTitle>
              {/* icon circle */}
              <Skeleton className="h-4 w-4 rounded" />
            </CardHeader>
            <CardContent className="flex items-center gap-3 pb-3">
              {/* circular chart placeholder */}
              <Skeleton className="h-[70px] w-[70px] rounded-full" />
              <div className="space-y-1 flex-1">
                <Skeleton className="h-5 w-20" />
                <Skeleton className="h-3 w-32" />
                <div className="flex gap-2 mt-2">
                  <Skeleton className="h-2 w-12" />
                  <Skeleton className="h-2 w-12" />
                </div>
              </div>
            </CardContent>
          </Card>

                    {/* Three small metric cards */}
          {[...Array(3)].map((_, i) => (
            <Card key={i} className="relative">
              <CardHeader className="flex flex-row items-center justify-between space-y-0 pb-1 pt-3">
                <CardTitle className="text-sm font-medium">
                  <Skeleton className="h-4 w-20" />
                </CardTitle>
                <Skeleton className="h-4 w-4 rounded" />
              </CardHeader>
              <CardContent className="space-y-2 pb-3">
                <Skeleton className="h-7 w-20" />
                <Skeleton className="h-3 w-24" />
              </CardContent>
            </Card>
          ))}
        </div>
      )
    }

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
  