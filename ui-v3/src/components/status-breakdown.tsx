"use client"

import { Card, CardContent, CardHeader, CardTitle } from "@/components/ui/card"
import { Bar, BarChart, ResponsiveContainer, XAxis, YAxis, Tooltip, Legend, CartesianGrid } from "recharts"
import { QueryStatusDistributionResult } from "@/lib/types";


interface StatusBreakdownProps {
  statusData: QueryStatusDistributionResult[]
}

export function StatusBreakdown({ statusData }: StatusBreakdownProps) {
  return (
    <Card>
      <CardHeader>
        <CardTitle>Status Code Distribution</CardTitle>
      </CardHeader>
      <CardContent>
        <div className="h-[300px] w-full">
          <ResponsiveContainer width="100%" height="100%">
            <BarChart data={statusData} margin={{ top: 0, right: 16, left: 0 }} barGap={2}>
              <CartesianGrid strokeDasharray="3 3" stroke="#888888" opacity={0.2} />
              <XAxis
                dataKey="hour"
                stroke="#888888"
                fontSize={12}
                tickLine={false}
                axisLine={false}
                tickFormatter={(value) => {
                  // Format to show just the hour like "03:00"
                  const date = new Date(value);
                  return date.toLocaleTimeString('en-US', {
                    hour: '2-digit',
                    minute: '2-digit',
                    hour12: false
                  });
                }}
                angle={-45} // Rotate labels 45 degrees
                textAnchor="end" // Align the rotated text
                height={60} // Provide space for rotated labels
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
                    const time = new Date(payload[0].payload.hour).toLocaleString('en-US', {
                      year: 'numeric',
                      month: '2-digit',
                      day: '2-digit',
                      hour: '2-digit',
                      minute: '2-digit',
                      second: '2-digit',
                      hour12: false
                    });

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

