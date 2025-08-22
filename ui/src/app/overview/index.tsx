import { KeyMetrics } from "@/components/key-metrics";
import { StatusBreakdown } from "@/components/status-breakdown";
import { QueryLatencyTrends } from "@/components/query-latency-trends";
import { QueryThroughputAnalysis } from "@/components/query-performance-analysis";
import { QueryErrorAnalysis } from "@/components/query-error-analysis";
import { QueryTable } from "@/components/query-table";
import { TableProvider } from "@/contexts/table-context";
import QueryTimeRangeDistribution from "@/components/query-time-range-distribution";

function OverviewHeader() {
  return (
    <div className="mb-6">
      <h1 className="text-2xl font-bold">Query Analytics</h1>
    </div>
  );
}

function OverviewContent() {
  return (
    <div className="mx-auto p-6">
      <OverviewHeader />
      <div className="grid gap-6">
        <KeyMetrics />

        <QueryTimeRangeDistribution />
        {/* Charts */}
        <div className="grid gap-6 lg:grid-cols-2">
          <StatusBreakdown />
          <QueryLatencyTrends />
          <QueryThroughputAnalysis />
          <QueryErrorAnalysis />
        </div>

        {/* Recent queries table */}
        <QueryTable />
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
