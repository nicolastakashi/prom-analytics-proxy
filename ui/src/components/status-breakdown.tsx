'use client';

import { Card, CardContent, CardHeader, CardTitle } from '@/components/ui/card';
import {
  Bar,
  BarChart,
  ResponsiveContainer,
  XAxis,
  YAxis,
  Tooltip,
  CartesianGrid,
} from 'recharts';
import { QueryStatusDistributionResult } from '@/lib/types';
import { formatTimestampByGranularity } from '@/lib/utils/date-formatting';

import { useQuery } from '@tanstack/react-query';
import { useDateRange } from '@/contexts/date-range-context';
import { getQueryStatusDistribution } from '@/api/queries';
import { Skeleton } from '@/components/ui/skeleton';

interface StatusBreakdownProps {
  fingerprint?: string;
}

export function StatusBreakdown({ fingerprint }: StatusBreakdownProps) {
  const { dateRange } = useDateRange();
  const from = dateRange?.from?.toISOString();
  const to = dateRange?.to?.toISOString();
  const fromDate = dateRange?.from ?? new Date();
  const toDate = dateRange?.to ?? new Date();

  const { data: statusData, isLoading } = useQuery<
    QueryStatusDistributionResult[]
  >({
    queryKey: ['queryStatusDistribution', from, to, fingerprint],
    queryFn: () => getQueryStatusDistribution(from, to, fingerprint),
    enabled: Boolean(from && to),
  });

  if (isLoading || !statusData) {
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
        <CardTitle>Status Code Distribution</CardTitle>
      </CardHeader>
      <CardContent>
        <div className="h-[300px] w-full">
          <ResponsiveContainer width="100%" height="100%">
            <BarChart
              data={statusData}
              margin={{ top: 0, right: 16, left: 0, bottom: 0 }}
              barGap={2}
            >
              <CartesianGrid
                strokeDasharray="3 3"
                stroke="#888888"
                opacity={0.2}
              />
              <XAxis
                dataKey="time"
                stroke="#888888"
                fontSize={12}
                tickLine={false}
                axisLine={false}
                tickFormatter={(value) =>
                  formatTimestampByGranularity(value, fromDate, toDate)
                }
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
                            <span className="text-[0.70rem] uppercase text-muted-foreground">
                              Time
                            </span>
                            <span className="font-bold text-muted-foreground">
                              {time}
                            </span>
                          </div>
                          {payload.map((entry) => (
                            <div key={entry.name} className="flex flex-col">
                              <span className="text-[0.70rem] uppercase text-muted-foreground">
                                {entry.name}
                              </span>
                              <span className="font-bold">
                                {entry.value}{' '}
                                {entry.value === 1 ? 'request' : 'requests'}
                              </span>
                            </div>
                          ))}
                        </div>
                      </div>
                    );
                  }
                  return null;
                }}
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
        <div className="mt-4 flex items-center justify-center gap-4">
          <div className="flex items-center gap-2">
            <div className="h-3 w-3 rounded-full bg-[hsl(var(--primary))]" />
            <span className="text-sm">Success</span>
          </div>
          <div className="flex items-center gap-2">
            <div className="h-3 w-3 rounded-full bg-[hsl(var(--warning))]" />
            <span className="text-sm">Client Error</span>
          </div>
          <div className="flex items-center gap-2">
            <div className="h-3 w-3 rounded-full bg-[hsl(var(--destructive))]" />
            <span className="text-sm">Server Error</span>
          </div>
        </div>
      </CardContent>
    </Card>
  );
}
