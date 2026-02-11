"use client";

import { Card, CardContent, CardHeader, CardTitle } from "@/components/ui/card";
import { Clock } from "lucide-react";
import { useQuery } from "@tanstack/react-query";
import { useDateRange } from "@/contexts/date-range-context";
import { getQueryTimeRangeDistribution } from "@/api/queries";
import { QueryTimeRangeDistributionResult } from "@/lib/types";
import { Skeleton } from "@/components/ui/skeleton";

const BUCKET_ORDER = ["<24h", "24h", "7d", "30d", "60d", "90d+"] as const;
const BUCKET_COLORS: Record<string, string> = {
  "<24h": "bg-blue-500",
  "24h": "bg-green-500",
  "7d": "bg-yellow-500",
  "30d": "bg-orange-500",
  "60d": "bg-red-500",
  "90d+": "bg-purple-500",
};

export default function QueryTimeRangeDistribution({
  fingerprint,
}: {
  fingerprint?: string;
}) {
  const { dateRange } = useDateRange();
  const from = dateRange?.from?.toISOString();
  const to = dateRange?.to?.toISOString();

  const { data, isLoading } = useQuery<QueryTimeRangeDistributionResult[]>({
    queryKey: ["queryTimeRangeDistribution", from, to, fingerprint],
    queryFn: () => getQueryTimeRangeDistribution(from, to, fingerprint),
    enabled: Boolean(from && to),
  });

  const buckets = BUCKET_ORDER.map((label) => {
    const found = data?.find((b) => b.label === label);
    return {
      range: label,
      percentage: found?.percent ?? 0,
      queries: found?.count ?? 0,
      color: BUCKET_COLORS[label],
    };
  });

  // Calculate dynamic retention insights
  const shortTerm = buckets
    .slice(0, 2)
    .reduce((acc, b) => acc + b.percentage, 0); // <24h + 24h
  const mediumTerm = buckets
    .slice(2, 4)
    .reduce((acc, b) => acc + b.percentage, 0); // 7d + 30d
  const longTerm = buckets.slice(4).reduce((acc, b) => acc + b.percentage, 0); // 60d + 90d+

  const getRetentionInsight = () => {
    const total = buckets.reduce((acc, b) => acc + b.queries, 0);
    if (total === 0) return null;

    if (shortTerm >= 70) {
      return `${shortTerm.toFixed(0)}% of queries use ranges ≤24h, suggesting significant potential for shorter retention periods to optimize storage costs.`;
    } else if (shortTerm + mediumTerm >= 80) {
      return `${(shortTerm + mediumTerm).toFixed(0)}% of queries use ranges ≤30d, indicating moderate optimization potential with 30-day retention.`;
    } else if (longTerm >= 40) {
      return `${longTerm.toFixed(0)}% of queries require long-term data (60d+), suggesting current retention policies are well-aligned with usage patterns.`;
    } else {
      return `Query ranges are well-distributed across time periods, indicating balanced retention requirements.`;
    }
  };

  const retentionInsight = getRetentionInsight();

  return (
    <Card>
      <CardHeader>
        <CardTitle className="flex items-center gap-2">
          <Clock className="h-5 w-5" />
          Query Time Range Distribution
        </CardTitle>
      </CardHeader>
      <CardContent>
        {isLoading ? (
          <div className="grid grid-cols-2 gap-4 md:grid-cols-3 lg:grid-cols-6">
            {Array.from({ length: 6 }).map((_, i) => (
              <div key={i} className="space-y-2">
                <div className="flex items-center justify-between">
                  <Skeleton className="h-4 w-10" />
                  <Skeleton className="h-4 w-8" />
                </div>
                <div className="h-2 rounded-full overflow-hidden bg-foreground/10">
                  <Skeleton className="h-2 w-full" />
                </div>
                <Skeleton className="h-3 w-20" />
              </div>
            ))}
          </div>
        ) : (
          <>
            <div className="grid grid-cols-2 gap-4 md:grid-cols-3 lg:grid-cols-6">
              {buckets.map((item) => (
                <div key={item.range} className="space-y-2">
                  <div className="flex items-center justify-between">
                    <span className="text-sm font-medium">{item.range}</span>
                    <span className="text-sm text-muted-foreground">
                      {item.percentage.toFixed(0)}%
                    </span>
                  </div>
                  <div className="h-2 rounded-full overflow-hidden bg-foreground/10">
                    <div
                      className={`h-full ${item.color} transition-all duration-300`}
                      style={{ width: `${item.percentage}%` }}
                    />
                  </div>
                  <div className="text-xs text-muted-foreground">
                    {item.queries.toLocaleString()} queries
                  </div>
                </div>
              ))}
            </div>
            {retentionInsight && (
              <div className="mt-4 p-3 bg-foreground/5 rounded-lg">
                <p className="text-sm text-muted-foreground">
                  <strong>Retention Insight:</strong> {retentionInsight}
                </p>
              </div>
            )}
          </>
        )}
      </CardContent>
    </Card>
  );
}
