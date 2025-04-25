"use client"

import { Card, CardContent, CardHeader, CardTitle } from "@/components/ui/card"
import { Bar, BarChart, ResponsiveContainer, XAxis, YAxis, Tooltip, Legend, CartesianGrid } from "recharts"
import { QueryStatusDistributionResult } from "@/lib/types"
import { formatTimestampByGranularity } from "@/lib/utils/date-formatting"

interface StatusBreakdownProps {
  statusData: QueryStatusDistributionResult[]
  from: Date
  to: Date
}

export function StatusBreakdown({ statusData, from, to }: StatusBreakdownProps) {
  return (
    <Card>
      <CardHeader>
        <CardTitle>Status Code Distribution</CardTitle>
      </CardHeader>
      <CardContent>
        <div className="h-[300px] w-full">
          <ResponsiveContainer width="100%" height="100%">
            <BarChart data={statusData} margin={{ top: 16, right: 0, left: 0 }} barGap={2}>
              <CartesianGrid strokeDasharray="3 3" stroke="#888888" opacity={0.2} />
              <XAxis
                dataKey="time"
                stroke="#888888"
                fontSize={12}
                tickLine={false}
                axisLine={false}
                tickFormatter={(value) => formatTimestampByGranularity(value, from, to)}
                angle={-45}
                textAnchor="end"
                height={60}
              />
              <YAxis
                stroke="#888888"
                fontSize={12}
                tickLine={false}
                axisLine={false}
                tickFormatter={(value) => `${value}`}
              />
              <Tooltip
                content={({ active, payload }) => {
                  if (active && payload && payload.length) {
                    const time = payload[0].payload.time;

                    return (
                      <div className="rounded-lg border bg-background p-2 shadow-sm">
                        <div className="grid gap-2">
                          <div className="flex flex-col">
                            <span className="text-[0.70rem] uppercase text-muted-foreground">Time</span>
                            <span className="font-bold text-muted-foreground">{time}</span>
                          </div>
                          {payload.map((entry) => (
                            <div key={entry.name} className="flex flex-col">
                              <span className="text-[0.70rem] uppercase text-muted-foreground">
                                {entry.name}
                              </span>
                              <span className="font-bold">
                                {entry.value} {entry.value === 1 ? 'request' : 'requests'}
                              </span>
                            </div>
                          ))}
                        </div>
                      </div>
                    )
                  }
                  return null
                }}
              />
              <Legend
                verticalAlign="top"
                height={36}
                formatter={(value) => <span className="text-sm text-muted-foreground">{value}</span>}
              />
              <Bar
                dataKey="2xx"
                name="Success"
                fill="hsl(var(--primary))"
                radius={[4, 4, 0, 0]}
              />
              <Bar
                dataKey="4xx"
                name="Client Error"
                fill="hsl(var(--warning))"
                radius={[4, 4, 0, 0]}
              />
              <Bar
                dataKey="5xx"
                name="Server Error"
                fill="hsl(var(--destructive))"
                radius={[4, 4, 0, 0]}
              />
            </BarChart>
          </ResponsiveContainer>
        </div>
      </CardContent>
    </Card>
  )
}

