import { KeyMetrics } from "@/components/key-metrics";
import { useDateRange } from "@/contexts/date-range-context";
import { useOverviewData } from "./use-overview-data";
import { LoadingState } from "./loading";
import { toast } from "sonner";
import { StatusBreakdown } from "@/components/status-breakdown";
import { QueryLatencyTrends } from "@/components/query-latency-trends";
import { QueryThroughputAnalysis } from "@/components/query-performance-analysis";

export function Overview() {
  const { dateRange } = useDateRange();
  const { 
    data: overviewData, 
    isLoading, 
    error 
  } = useOverviewData(dateRange);

  if (isLoading) {
    return <LoadingState />;
  }

  console.log(overviewData);

  if (error) {
    toast.error("Failed to fetch data", {
      description: error instanceof Error ? error.message : "An unexpected error occurred",
    });
    
    return (
      <div className="flex h-[50vh] w-full items-center justify-center">
        <div className="text-center">
          <h2 className="text-2xl font-bold text-red-600">Failed to load data</h2>
          <p className="mt-2 text-gray-600">
            {error instanceof Error ? error.message : "An unexpected error occurred"}
          </p>
        </div>
      </div>
    );
  }
  
  return (
    <div className="mx-auto p-6">
      <div className="mb-6">
        <h1 className="text-2xl font-bold">Query Analytics</h1>
      </div>
      <div className="grid gap-6">
        <KeyMetrics 
          queryTypes={overviewData.queryTypes}
          averageDuration={overviewData.averageDuration}
          queryRate={overviewData.queryRate}
        />
        <div className="grid gap-6 lg:grid-cols-2">
          <>
            <StatusBreakdown statusData={overviewData.queryStatusDistribution || []} />
            <QueryLatencyTrends latencyTrendsData={overviewData.queryLatencyTrends || []} />
          </>
          <div className="grid gap-6">
            <QueryThroughputAnalysis throughputData={overviewData.queryThroughputAnalysis || []} />
            {/* <QueryErrorAnalysis /> */}
          </div>
        </div>
        {/* <QueryTable /> */}
      </div>
    </div>
  );
}