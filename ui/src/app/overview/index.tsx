import { KeyMetrics } from '@/components/key-metrics';
import { StatusBreakdown } from '@/components/status-breakdown';
import { QueryLatencyTrends } from '@/components/query-latency-trends';
import { QueryThroughputAnalysis } from '@/components/query-performance-analysis';
import { QueryErrorAnalysis } from '@/components/query-error-analysis';
import { TableProvider } from '@/contexts/table-context';
import QueryTimeRangeDistribution from '@/components/query-time-range-distribution';

function OverviewHeader() {
  return (
    <div className="mb-4">
      <h1 className="text-2xl font-bold">Overview</h1>
      <p className="text-sm text-muted-foreground">
        Aggregated view of query executions, latency, errors, throughput, and
        status distribution.
      </p>
    </div>
  );
}

function OverviewContent() {
  return (
    <div className="mx-auto p-6">
      <OverviewHeader />
      <div className="grid gap-4">
        <KeyMetrics />
        <QueryTimeRangeDistribution />
        {/* Charts */}
        <div className="grid gap-4 lg:grid-cols-2">
          <StatusBreakdown />
          <QueryLatencyTrends />
          <QueryThroughputAnalysis />
          <QueryErrorAnalysis />
        </div>

        {/* Recent queries moved to Queries page */}
      </div>
    </div>
  );
}

export function Overview() {
  return (
    <TableProvider>
      <OverviewContent />
    </TableProvider>
  );
}
