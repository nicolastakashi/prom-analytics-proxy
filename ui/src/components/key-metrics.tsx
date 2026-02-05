'use client';

import { Card, CardContent, CardHeader, CardTitle } from '@/components/ui/card';
import {
  AverageDurationResponse,
  QueryRateResponse,
  QueryTypesResponse,
} from '@/lib/types';
import { formatDuration } from '@/lib/utils';
import { Activity, AlertTriangle, Clock, Filter } from 'lucide-react';
import { PieChart, Pie, ResponsiveContainer, Cell, Tooltip } from 'recharts';

const COLORS = ['hsl(var(--primary))', 'hsl(var(--primary) / 0.3)'];

import { useQuery } from '@tanstack/react-query';
import { useDateRange } from '@/contexts/date-range-context';
import { getQueryTypes, getAverageDuration, getQueryRate } from '@/api/queries';
import { Skeleton } from '@/components/ui/skeleton';

export function KeyMetrics({ fingerprint }: { fingerprint?: string }) {
  const { dateRange } = useDateRange();
  const from = dateRange?.from?.toISOString();
  const to = dateRange?.to?.toISOString();

  const { data: queryTypes, isLoading: isLoadingTypes } =
    useQuery<QueryTypesResponse>({
      queryKey: ['queryTypes', from, to, fingerprint],
      queryFn: () => getQueryTypes(from, to, fingerprint),
      enabled: Boolean(from && to),
    });

  const { data: averageDuration, isLoading: isLoadingAvg } =
    useQuery<AverageDurationResponse>({
      queryKey: ['averageDuration', from, to, fingerprint],
      queryFn: () => getAverageDuration(from, to, fingerprint),
      enabled: Boolean(from && to),
    });

  const { data: queryRate, isLoading: isLoadingRate } =
    useQuery<QueryRateResponse>({
      queryKey: ['queryRate', from, to, fingerprint],
      queryFn: () => getQueryRate(from, to, fingerprint),
      enabled: Boolean(from && to),
    });

  const loading = isLoadingTypes || isLoadingAvg || isLoadingRate;

  if (loading || !queryTypes || !averageDuration || !queryRate) {
    return (
      <div className="grid gap-4 md:grid-cols-2 lg:grid-cols-4 xl:grid-cols-5">
        {/* Large card placeholder */}
        <Card className="md:col-span-2 lg:col-span-2 xl:col-span-2 relative py-2 gap-1 md:h-[128px] lg:h-[132px]">
          {/* title & icon */}
          <CardHeader className="flex flex-row items-center justify-between space-y-0 pb-0 pt-0">
            <CardTitle className="text-sm font-medium">
              <Skeleton className="h-4 w-24" />
            </CardTitle>
            {/* icon circle */}
            <Skeleton className="h-5 w-5 rounded" />
          </CardHeader>
          <CardContent className="flex items-center gap-4 pb-2 max-sm:flex-col max-sm:items-start">
            {/* circular chart placeholder */}
            <Skeleton className="h-[76px] w-[76px] sm:h-[92px] sm:w-[92px] rounded-full" />
            <div className="space-y-1 flex-1 min-w-0">
              <Skeleton className="h-7 w-24" />
              <Skeleton className="h-4 w-40" />
              <div className="flex gap-2 mt-2">
                <Skeleton className="h-2 w-12" />
                <Skeleton className="h-2 w-12" />
              </div>
            </div>
          </CardContent>
        </Card>

        {/* Three small metric cards */}
        {[...Array(3)].map((_, i) => (
          <Card
            key={i}
            className="relative py-2 gap-3 md:h-[128px] lg:h-[132px]"
          >
            <CardHeader className="flex flex-row items-center justify-between space-y-0 pb-0 pt-1">
              <CardTitle className="text-sm font-medium">
                <Skeleton className="h-4 w-20" />
              </CardTitle>
              <Skeleton className="h-5 w-5 rounded" />
            </CardHeader>
            <CardContent className="space-y-2 pb-2">
              <Skeleton className="h-8 w-24" />
              <Skeleton className="h-4 w-28" />
            </CardContent>
          </Card>
        ))}
      </div>
    );
  }

  let queryTypeData: { name: string; value: number }[] = [];
  if (queryTypes) {
    queryTypeData = [
      { name: 'Instant', value: queryTypes.instant_percent },
      { name: 'Range', value: queryTypes.range_percent },
    ];
  }

  return (
    <div className="grid gap-4 md:grid-cols-2 lg:grid-cols-4 xl:grid-cols-5">
      <Card className="md:col-span-2 lg:col-span-2 xl:col-span-2 gap-1 py-2 md:h-[128px] lg:h-[132px]">
        <CardHeader className="flex flex-row items-center justify-between space-y-0 pb-0 pt-0">
          <CardTitle className="text-sm font-medium">Query Types</CardTitle>
          <Filter className="h-5 w-5 text-muted-foreground" />
        </CardHeader>
        <CardContent className="pb-2">
          <div className="flex items-center gap-4 max-sm:flex-col max-sm:items-start">
            <div className="h-[76px] w-[76px] sm:h-[92px] sm:w-[92px]">
              <ResponsiveContainer width="100%" height="100%">
                <PieChart>
                  <Pie
                    cx="50%"
                    cy="50%"
                    data={queryTypeData}
                    innerRadius="58%"
                    outerRadius="82%"
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
                                <span className="font-bold">
                                  {parseFloat(
                                    payload[0].value as string,
                                  ).toFixed(2)}
                                  %
                                </span>
                              </div>
                            </div>
                          </div>
                        );
                      }
                      return null;
                    }}
                  />
                </PieChart>
              </ResponsiveContainer>
            </div>
            <div className="space-y-0.5 min-w-0">
              <p className="text-2xl font-bold leading-none">
                {(queryTypes?.total_queries ?? 0).toLocaleString()}
              </p>
              <p className="text-xs text-muted-foreground">
                Total queries in selected period
              </p>
              <div className="mt-1 flex items-center gap-3 text-[11px]">
                <div className="flex items-center gap-1">
                  <div className="h-2 w-2 rounded-full bg-chart-1" />
                  Instant (
                  {queryTypeData[0]?.value
                    ? parseFloat(queryTypeData[0].value.toString()).toFixed(2)
                    : 0}
                  %)
                </div>
                <div className="flex items-center gap-1">
                  <div className="h-2 w-2 rounded-full bg-chart-2" />
                  Range (
                  {queryTypeData[1]?.value
                    ? parseFloat(queryTypeData[1].value.toString()).toFixed(2)
                    : 0}
                  %)
                </div>
              </div>
            </div>
          </div>
        </CardContent>
      </Card>

      <Card className="gap-3 py-2 auto-rows-min">
        <CardHeader className="flex flex-row items-center justify-between space-y-0 pb-1 pt-1">
          <CardTitle className="text-sm font-medium">Avg Duration</CardTitle>
          <Clock className="h-5 w-5 text-muted-foreground" />
        </CardHeader>
        <CardContent className="pb-2">
          <div className="space-y-0.5 leading-tight pt-0.5">
            <p className="text-2xl font-bold leading-none">
              {formatDuration(averageDuration?.avg_duration || 0)}
            </p>
            <p className="text-xs text-muted-foreground mt-1">
              {averageDuration?.delta_percent != null
                ? `${averageDuration.delta_percent.toFixed(2)}% from previous period`
                : 'No previous data'}
            </p>
          </div>
        </CardContent>
      </Card>

      <Card className="gap-3 py-2 auto-rows-min">
        <CardHeader className="flex flex-row items-center justify-between space-y-0 pb-1 pt-1">
          <CardTitle className="text-sm font-medium">Success Rate</CardTitle>
          <Activity className="h-5 w-5 text-muted-foreground" />
        </CardHeader>
        <CardContent className="pb-2">
          <div className="space-y-0.5 leading-tight pt-0.5">
            <p className="text-2xl font-bold leading-none">
              {queryRate?.success_rate_percent != null
                ? `${queryRate.success_rate_percent.toFixed(2)}%`
                : '0%'}
            </p>
            <div className="flex items-center gap-1 text-[11px] mt-1">
              <div className="h-2 w-2 rounded-full bg-green-500" />
              <span className="text-muted-foreground">
                {(queryRate?.success_total ?? 0).toLocaleString()} successful
              </span>
            </div>
          </div>
        </CardContent>
      </Card>

      <Card className="gap-3 py-2 auto-rows-min">
        <CardHeader className="flex flex-row items-center justify-between space-y-0 pb-1 pt-1">
          <CardTitle className="text-sm font-medium">Error Rate</CardTitle>
          <AlertTriangle className="h-5 w-5 text-muted-foreground" />
        </CardHeader>
        <CardContent className="pb-2">
          <div className="space-y-0.5 leading-tight pt-0.5">
            <p className="text-2xl font-bold leading-none">
              {queryRate?.error_rate_percent != null
                ? `${queryRate.error_rate_percent.toFixed(2)}%`
                : '0%'}
            </p>
            <div className="flex items-center gap-1 text-[11px] mt-1">
              <div className="h-2 w-2 rounded-full bg-red-500" />
              <span className="text-muted-foreground">
                {(queryRate?.error_total ?? 0).toLocaleString()} failed
              </span>
            </div>
          </div>
        </CardContent>
      </Card>
    </div>
  );
}
