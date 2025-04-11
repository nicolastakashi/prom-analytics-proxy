"use client"

import { Card, CardContent, CardHeader, CardTitle } from "@/components/ui/card"
import { Area, AreaChart, ResponsiveContainer, Tooltip, XAxis, YAxis, CartesianGrid } from "recharts"
import { ArrowDown, ArrowUp, Info, X } from "lucide-react"
import { Button } from "@/components/ui/button"
import { Popover, PopoverContent, PopoverTrigger } from "@/components/ui/popover"
import React from "react"

interface InfoTooltipProps {
  content: string
}

function InfoTooltip({ content }: InfoTooltipProps) {
  const [isOpen, setIsOpen] = React.useState(false)

  return (
    <Popover open={isOpen} onOpenChange={setIsOpen}>
      <PopoverTrigger asChild>
        <Button variant="ghost" size="sm" className="h-11 w-11 rounded-full p-0 hover:bg-muted active:bg-muted/70">
          <Info className="h-5 w-5 text-muted-foreground" />
          <span className="sr-only">More information</span>
        </Button>
      </PopoverTrigger>
      <PopoverContent side="top" align="center" className="w-80">
        <div className="flex items-center justify-between">
          <p className="text-sm">{content}</p>
          <Button variant="ghost" size="sm" className="h-8 w-8 p-0 hover:bg-muted" onClick={() => setIsOpen(false)}>
            <X className="h-4 w-4" />
            <span className="sr-only">Close</span>
          </Button>
        </div>
      </PopoverContent>
    </Popover>
  )
}

const data = Array.from({ length: 24 }, (_, i) => ({
  hour: `${String(i).padStart(2, "0")}:00`,
  queryTime: Math.floor(50 + Math.random() * 150),
  samples: Math.floor(800000 + Math.random() * 400000),
  p95: Math.floor(80 + Math.random() * 200),
  p99: Math.floor(100 + Math.random() * 250),
}))

export function MetricPerformance() {
  return (
    <Card>
      <CardHeader>
        <CardTitle>Performance Statistics</CardTitle>
      </CardHeader>
      <CardContent>
        <div className="space-y-8">
          <div className="grid gap-4 md:grid-cols-3">
            <Card>
              <CardContent>
                <div className="space-y-2">
                  <div className="flex items-center gap-2">
                    <p className="text-sm text-muted-foreground">Total Queries</p>
                    <InfoTooltip content="Total number of queries executed using this metric in the selected time period" />
                  </div>
                  <div className="flex items-center gap-2">
                    <p className="text-2xl font-bold">12,421</p>
                    <div className="flex items-center text-xs text-green-500">
                      <ArrowUp className="h-3 w-3" />
                      5.2%
                    </div>
                  </div>
                </div>
              </CardContent>
            </Card>
            <Card>
              <CardContent>
                <div className="space-y-2">
                  <div className="flex items-center gap-2">
                    <p className="text-sm text-muted-foreground">Query Rate</p>
                    <InfoTooltip content="Number of queries per second being executed against this metric" />
                  </div>
                  <div className="flex items-center gap-2">
                    <p className="text-2xl font-bold">145</p>
                    <div className="flex items-center text-xs text-yellow-500">
                      <ArrowUp className="h-3 w-3" />
                      8%
                    </div>
                  </div>
                </div>
              </CardContent>
            </Card>
            <Card>
              <CardContent>
                <div className="space-y-2">
                  <div className="flex items-center gap-2">
                    <p className="text-sm text-muted-foreground">Error Rate</p>
                    <InfoTooltip content="Percentage of queries that failed or returned errors in the last 24 hours" />
                  </div>
                  <div className="flex items-center gap-2">
                    <p className="text-2xl font-bold">0.2%</p>
                    <div className="flex items-center text-xs text-green-500">
                      <ArrowDown className="h-3 w-3" />
                      0.1%
                    </div>
                  </div>
                </div>
              </CardContent>
            </Card>
          </div>

          <div className="grid gap-4 md:grid-cols-3">
            <Card>
              <CardContent>
                <div className="space-y-2">
                  <div className="flex items-center gap-2">
                    <p className="text-sm text-muted-foreground">Average Samples</p>
                    <InfoTooltip content="Average number of data points processed per query" />
                  </div>
                  <div className="flex items-center gap-2">
                    <p className="text-2xl font-bold">1.2M</p>
                    <div className="flex items-center text-xs text-green-500">
                      <ArrowDown className="h-3 w-3" />
                      3%
                    </div>
                  </div>
                </div>
              </CardContent>
            </Card>
            <Card>
              <CardContent>
                <div className="space-y-2">
                  <div className="flex items-center gap-2">
                    <p className="text-sm text-muted-foreground">Peak Samples</p>
                    <InfoTooltip content="Maximum number of data points processed in a single query" />
                  </div>
                  <div className="flex items-center gap-2">
                    <p className="text-2xl font-bold">2.5M</p>
                    <div className="flex items-center text-xs text-yellow-500">
                      <ArrowUp className="h-3 w-3" />
                      8%
                    </div>
                  </div>
                </div>
              </CardContent>
            </Card>
            <Card>
              <CardContent>
                <div className="space-y-2">
                  <div className="flex items-center gap-2">
                    <p className="text-sm text-muted-foreground">Sample Rate</p>
                    <InfoTooltip content="Number of new data points being ingested per second" />
                  </div>
                  <div className="flex items-center gap-2">
                    <p className="text-2xl font-bold">50K/s</p>
                    <div className="flex items-center text-xs text-green-500">
                      <ArrowDown className="h-3 w-3" />
                      2%
                    </div>
                  </div>
                </div>
              </CardContent>
            </Card>
          </div>

          <div>
            <h4 className="mb-4 font-medium">Query Duration Distribution</h4>
            <div className="h-[300px] w-full">
              <ResponsiveContainer width="100%" height="100%">
                <AreaChart data={data} margin={{ top: 0, right: 16, left: 0, bottom: 0 }}>
                  <CartesianGrid strokeDasharray="3 3" stroke="#888888" opacity={0.2} />
                  <XAxis
                    dataKey="hour"
                    stroke="#888888"
                    fontSize={12}
                    tickLine={false}
                    axisLine={false}
                    angle={-45}
                    textAnchor="end"
                    height={60}
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
                                <span className="font-bold text-muted-foreground">{payload[0].payload.hour}</span>
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
                    dataKey="queryTime"
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
          </div>
        </div>
      </CardContent>
    </Card>
  )
}
