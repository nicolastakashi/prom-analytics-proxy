"use client"

import { Card, CardContent, CardHeader, CardTitle } from "@/components/ui/card"
import { AverageDurationResponse, QueryTypesResponse } from "@/lib/types"
import { Activity, AlertTriangle, Clock, Filter, PieChart as PieChartIcon } from "lucide-react"
import { PieChart, Pie, ResponsiveContainer, Cell, Tooltip } from "recharts"

const COLORS = ["hsl(var(--primary))", "hsl(var(--primary) / 0.3)"]

interface KeyMetricsProps {
    queryTypes: QueryTypesResponse
    averageDuration: AverageDurationResponse
}

function formatDuration(ms: number): string {
    if (ms < 1) {
        return `${Math.round(ms * 1000)}µs`
    }
    if (ms < 1000) {
        return `${Math.round(ms)}ms`
    }
    if (ms < 60000) {
        return `${Math.round(ms / 1000)}s`
    }
    if (ms < 3600000) {
        return `${Math.round(ms / 60000)}m`
    }
    return `${Math.round(ms / 3600000)}h`
}

export function KeyMetrics(props: KeyMetricsProps) {
    const { queryTypes, averageDuration } = props

    let queryTypeData: { name: string, value: number }[] = []

    if (queryTypes) {
        queryTypeData = [
            { name: "Instant", value: queryTypes.instant_percent },
            { name: "Range", value: queryTypes.range_percent },
        ]
    }

    return (    
        <div className="grid gap-4 md:grid-cols-2 lg:grid-cols-5">
            <Card className="lg:col-span-2">
                <CardHeader className="flex flex-row items-center justify-between space-y-0 pb-2">
                    <CardTitle className="text-sm font-medium">Query Types</CardTitle>
                    <Filter className="h-4 w-4 text-muted-foreground" />
                </CardHeader>
                <CardContent>
                    <div className="flex items-center gap-4">
                        <div className="h-[80px] w-[80px]">
                            {queryTypeData.length === 0 || queryTypeData[0].value <= 0 || queryTypeData[1].value <= 0 ? (
                                
                                <div className="flex h-full w-full flex-col items-center justify-center text-muted-foreground">
                                    <PieChartIcon className="h-8 w-8 mb-2 opacity-50" />
                                </div>
                            ) : (
                                <ResponsiveContainer width="100%" height="100%">
                                    <PieChart>
                                        <Pie 
                                            data={queryTypeData} 
                                            innerRadius={25} 
                                            outerRadius={35} 
                                            paddingAngle={2} 
                                            dataKey="value"
                                        >
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
                                                                    <span className="font-bold">{payload[0].value}%</span>
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
                            )}
                        </div>
                        <div className="space-y-1">
                            <p className="text-xl font-bold">{queryTypes?.total_queries || 0}</p>
                            <p className="text-xs text-muted-foreground">Total queries in selected period</p>
                            <div className="mt-3 flex items-center gap-2 text-xs">
                                <div className="flex items-center gap-1">
                                    <div className="h-2 w-2 rounded-full bg-primary" />
                                    Instant ({queryTypeData[0].value}%)
                                </div>
                                <div className="flex items-center gap-1">
                                    <div className="h-2 w-2 rounded-full bg-primary/30" />
                                    Range ({queryTypeData[1].value}%)
                                </div>
                            </div>
                        </div>
                    </div>
                </CardContent>
            </Card>

            <Card>
                <CardHeader className="flex flex-row items-center justify-between space-y-0 pb-2">
                    <CardTitle className="text-sm font-medium">Avg Duration</CardTitle>
                    <Clock className="h-4 w-4 text-muted-foreground" />
                </CardHeader>
                <CardContent>
                    <div className="space-y-1">
                        <p className="text-2xl font-bold">
                            {formatDuration(averageDuration.avg_duration)}
                        </p>
                        <p className="text-xs text-muted-foreground">{averageDuration.delta_percent}% from previous period</p>
                    </div>
                </CardContent>
            </Card>

            <Card>
                <CardHeader className="flex flex-row items-center justify-between space-y-0 pb-2">
                    <CardTitle className="text-sm font-medium">Success Rate</CardTitle>
                    <Activity className="h-4 w-4 text-muted-foreground" />
                </CardHeader>
                <CardContent>
                    <div className="space-y-1">
                        <p className="text-2xl font-bold">99.8%</p>
                        <div className="flex items-center gap-1 text-xs">
                            <div className="h-2 w-2 rounded-full bg-green-500" />
                            <span className="text-muted-foreground">12,397 successful</span>
                        </div>
                    </div>
                </CardContent>
            </Card>

            <Card>
                <CardHeader className="flex flex-row items-center justify-between space-y-0 pb-2">
                    <CardTitle className="text-sm font-medium">Error Rate</CardTitle>
                    <AlertTriangle className="h-4 w-4 text-muted-foreground" />
                </CardHeader>
                <CardContent>
                    <div className="space-y-1">
                        <p className="text-2xl font-bold">0.2%</p>
                        <div className="flex items-center gap-1 text-xs">
                            <div className="h-2 w-2 rounded-full bg-red-500" />
                            <span className="text-muted-foreground">24 failed</span>
                        </div>
                    </div>
                </CardContent>
            </Card>
        </div>
    )
}

