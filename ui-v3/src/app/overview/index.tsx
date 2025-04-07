import { KeyMetrics } from "@/components/key-metrics";
import { useDateRange } from "@/contexts/date-range-context";
import { useOverviewData } from "./use-overview-data";
import { LoadingState } from "./loading";
import { toast } from "sonner";
import { StatusBreakdown } from "@/components/status-breakdown";
import { QueryLatencyTrends } from "@/components/query-latency-trends";
import { QueryThroughputAnalysis } from "@/components/query-performance-analysis";
import { QueryErrorAnalysis } from "@/components/query-error-analysis";
import { QueryTable } from "@/components/query-table";
import { TableProvider, useTable } from "@/contexts/table-context";

function ErrorDisplay({ error }: { error: Error | unknown }) {
  const errorMessage = error instanceof Error ? error.message : "An unexpected error occurred";
  
  toast.error("Failed to fetch data", {
    description: errorMessage,
  });
  
  return (
    <div className="flex h-[50vh] w-full items-center justify-center">
      <div className="text-center">
        <h2 className="text-2xl font-bold text-red-600">Failed to load data</h2>
        <p className="mt-2 text-gray-600">{errorMessage}</p>
      </div>
    </div>
  );
}

function OverviewHeader() {
  return (
    <div className="mb-6">
      <h1 className="text-2xl font-bold">Query Analytics</h1>
    </div>
  );
}

// eslint-disable-next-line @typescript-eslint/no-explicit-any
function OverviewMetrics({ overviewData }: { overviewData: any }) {
  return (
    <KeyMetrics 
      queryTypes={overviewData.queryTypes}
      averageDuration={overviewData.averageDuration}
      queryRate={overviewData.queryRate}
    />
  );
}

// eslint-disable-next-line @typescript-eslint/no-explicit-any
function OverviewCharts({ overviewData, dateRange }: { overviewData: any; dateRange: any }) {
  return (
    <div className="grid gap-6 lg:grid-cols-2">
      <StatusBreakdown 
        statusData={overviewData.queryStatusDistribution || []} 
        from={dateRange?.from || new Date()} 
        to={dateRange?.to || new Date()} 
      />
      <QueryLatencyTrends 
        latencyTrendsData={overviewData.queryLatencyTrends || []} 
        from={dateRange?.from || new Date()} 
        to={dateRange?.to || new Date()} 
      />
      <QueryThroughputAnalysis 
        throughputData={overviewData.queryThroughputAnalysis || []} 
        from={dateRange?.from || new Date()} 
        to={dateRange?.to || new Date()} 
      />
      <QueryErrorAnalysis 
        data={overviewData.queryErrorAnalysis || []} 
        from={dateRange?.from || new Date()} 
        to={dateRange?.to || new Date()} 
      />
    </div>
  );
}

function OverviewContent() {
  const { dateRange } = useDateRange();
  const { tableState } = useTable();
  const { data: overviewData, isLoading, error } = useOverviewData(dateRange, tableState);

  if (isLoading) {
    return <LoadingState />;
  }

  if (error) {
    return <ErrorDisplay error={error} />;
  }
  
  return (
    <div className="mx-auto p-6">
      <OverviewHeader />
      <div className="grid gap-6">
        <OverviewMetrics overviewData={overviewData} />
        <OverviewCharts overviewData={overviewData} dateRange={dateRange} />
        <QueryTable data={overviewData.recentQueries} />
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