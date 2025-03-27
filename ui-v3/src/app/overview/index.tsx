import { FilterPanel } from "@/components/filter-panel";
import { KeyMetrics } from "@/components/key-metrics";
import { useDateRange } from "@/contexts/date-range-context";
import { useOverviewData } from "./use-overview-data";
import { LoadingState } from "./loading";
import { toast } from "sonner";
import { StatusBreakdown } from "@/components/status-breakdown";
import { useQueryOverviewData } from "./use-query-overview-data";
import { QueryLatencyTrends } from "@/components/query-latency-trends";

export function Overview() {
  const { dateRange } = useDateRange();
  const { data: overviewData, isLoading: isLoadingOverviewData, error: errorOverviewData } = useOverviewData(dateRange);
  const { data: queryOverviewData, isLoading: isLoadingQueryOverviewData, error: errorQueryOverviewData } = useQueryOverviewData(dateRange);

  if (isLoadingOverviewData || isLoadingQueryOverviewData) {
    return <LoadingState />;
  }

  console.log(queryOverviewData);

  if (errorOverviewData || errorQueryOverviewData) {
    toast.error("Failed to fetch data", {
      description: errorOverviewData instanceof Error ? errorOverviewData.message : "An unexpected error occurred",
    });
    
    return (
      <div className="flex h-[50vh] w-full items-center justify-center">
        <div className="text-center">
          <h2 className="text-2xl font-bold text-red-600">Failed to load data</h2>
          <p className="mt-2 text-gray-600">
            {errorOverviewData instanceof Error ? errorOverviewData.message : "An unexpected error occurred"}
          </p>
        </div>
      </div>
    );
  }
  
  return (
    <div className="mx-auto p-6">
      <div className="mb-6 flex items-center justify-between">
        <h1 className="text-2xl font-bold">Query Analytics</h1>
        <FilterPanel />
      </div>
      <div className="grid gap-6">
        <KeyMetrics {...overviewData} />
        <div className="grid gap-6 lg:grid-cols-2">
          <>
            <StatusBreakdown statusData={queryOverviewData?.queryStatusDistribution || []} />
            <QueryLatencyTrends latencyTrendsData={queryOverviewData?.queryLatencyTrends || []} />
          </>
          <div className="grid gap-6">
            {/* <QueryPerformanceAnalysis />
            <QueryErrorAnalysis /> */}
          </div>
        </div>
        {/* <QueryTable /> */}
      </div>
    </div>
  );
}