'use client';

import { useParams } from 'wouter';
import { MetricDetailHeader } from '@/components/metrics-explorer/metric-detail-header';
import { useDateRange } from '@/contexts/date-range-context';
import { Tabs, TabsList, TabsTrigger, TabsContent } from '@/components/ui/tabs';
import { MetricStats } from '@/components/metrics-explorer/metric-stats';
import { MetricPerformance } from '@/components/metrics-explorer/metric-performance';
import { MetricProducers } from '@/components/metrics-explorer/metric-producer';
import MetricUsage from '@/components/metrics-explorer/metric-usage';

export default function MetricsDetails() {
  const params = useParams();
  const { metric } = params;
  const { dateRange } = useDateRange();

  if (!metric) return null;

  return (
    <div className="p-6">
      <MetricDetailHeader metricName={metric} />

      {/* Stats Grid */}
      <div className="mt-6">
        <MetricStats metricName={metric} />
      </div>

      {/* Tabs */}
      <div className="mt-6">
        <Tabs defaultValue="performance">
          <TabsList className="flex bg-gray-100 rounded-lg overflow-hidden">
            <TabsTrigger value="performance" className="flex-1 py-3 px-5">
              Performance
            </TabsTrigger>
            <TabsTrigger value="producers" className="flex-1 py-3 px-5">
              Producers
            </TabsTrigger>
            <TabsTrigger value="usage" className="flex-1 py-3 px-5">
              Usage
            </TabsTrigger>
          </TabsList>

          <TabsContent value="performance" className="mt-2">
            <MetricPerformance metricName={metric} />
          </TabsContent>

          <TabsContent value="producers" className="mt-2">
            <MetricProducers metricName={metric} />
          </TabsContent>

          <TabsContent value="usage" className="mt-2">
            <MetricUsage metricName={metric} dateRange={dateRange} />
          </TabsContent>
        </Tabs>
      </div>
    </div>
  );
}
