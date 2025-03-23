import { FilterPanel } from "@/components/filter-panel";
import { KeyMetrics } from "@/components/key-metrics";
import { useDateRange } from "@/contexts/date-range-context";
import { useOverviewData } from "./use-overview-data";

export function Overview() {
  const { dateRange } = useDateRange();
  const { data, isLoading } = useOverviewData(dateRange);

  if (isLoading) {
    return <div>Loading...</div>;
  }
  
  return (
    <div className="mx-auto pl-6 pr-6">
      <div className="mb-6 flex items-center justify-between">
        <h1 className="text-2xl font-bold">Query Analytics</h1>
        <FilterPanel />
      </div>
      <div className="grid gap-6">
        <KeyMetrics {...data} />
        <div className="grid gap-6 lg:grid-cols-2">
          <div className="grid gap-6">
            {/* <StatusBreakdown /> */}
            {/* <QueryPerformanceAnalysis /> */}
          </div>
          <div className="grid gap-6">
            {/* <QueryPerformanceAnalysis /> */}
            {/* <QueryPerformanceAnalysis /> */}
          </div>
        </div>
        {/* <QueryTable /> */}
      </div>
    </div>
  );
}