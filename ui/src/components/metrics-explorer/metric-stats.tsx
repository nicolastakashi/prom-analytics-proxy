"use client"

import { Card, CardContent, CardHeader, CardTitle } from "@/components/ui/card";
import { Bell, BarChart3, Database, GitMerge } from "lucide-react";
import { Skeleton } from "@/components/ui/skeleton";
import { useMetricStatistics } from "@/app/metrics/use-metrics-data";
import { useDateRange } from "@/contexts/date-range-context";
import { formatUnit } from "@/lib/utils";

interface MetricStatsProps {
  metricName: string;
}

export function MetricStats({ metricName }: MetricStatsProps) {
  const { dateRange } = useDateRange();
  const { data, isLoading, error } = useMetricStatistics(metricName, dateRange);

  if (isLoading) {
    return (
      <div className="grid gap-4 md:grid-cols-4">
        {[...Array(4)].map((_, i) => (
          <Card key={i}>
            <CardHeader className="flex flex-row items-center justify-between space-y-0 pb-1 pt-3">
              <CardTitle className="text-sm font-medium">
                <Skeleton className="h-4 w-24" />
              </CardTitle>
              <Skeleton className="h-4 w-4" />
            </CardHeader>
            <CardContent className="space-y-2 pb-3">
              <Skeleton className="h-6 w-20" />
              <Skeleton className="h-3 w-24" />
            </CardContent>
          </Card>
        ))}
      </div>
    );
  }

  if (error || !data.statistics) {
    return null;
  }

  const stats = data.statistics;

  return (
    <div className="grid gap-4 md:grid-cols-4">
      <StatCard
        title="Series Count"
        value={formatUnit(stats.serieCount)}
        subtitle={`across ${formatUnit(stats.labelCount)} labels`}
        icon={Database}
      />
      <StatCard
        title="Alerts"
        value={formatUnit(stats.alertCount)}
        subtitle={`across ${formatUnit(stats.totalAlerts)} alerts`}
        icon={Bell}
      />
      <StatCard
        title="Recording Rules"
        value={formatUnit(stats.recordCount)}
        subtitle={`across ${formatUnit(stats.totalRecords)} records`}
        icon={GitMerge}
      />
      <StatCard
        title="Dashboards"
        value={formatUnit(stats.dashboardCount)}
        subtitle={`across ${formatUnit(stats.totalDashboards)} dashboards`}
        icon={BarChart3}
      />
    </div>
  );
}

interface StatCardProps {
  title: string;
  value: string | number;
  subtitle?: string;
  icon: React.ElementType;
}

function StatCard({ title, value, subtitle, icon: Icon }: StatCardProps) {
  return (
    <Card>
      <CardContent className="p-4">
        <div className="flex flex-col gap-1">
          <div className="flex items-center justify-between">
            <span className="text-sm text-muted-foreground">{title}</span>
            <Icon className="h-4 w-4 text-muted-foreground" />
          </div>
          <div className="flex items-baseline gap-2">
            <span className="text-2xl font-bold">{value}</span>
          </div>
          {subtitle && <span className="text-xs text-muted-foreground">{subtitle}</span>}
        </div>
      </CardContent>
    </Card>
  );
}

