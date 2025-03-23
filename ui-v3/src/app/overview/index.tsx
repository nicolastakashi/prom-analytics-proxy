import { FilterPanel } from "@/components/filter-panel";
import { KeyMetrics } from "@/components/key-metrics";

export function Overview() {

  const queryTypes = {
    instant_percent: 0.5,
    range_percent: 0.5,
    total_queries: 1000,
    instant_queries: 500,
    range_queries: 500,
  }

  const averageDuration = {
    avg_duration: 1000,
    delta_percent: 0.5,
  }

  const queryRate = {
    success_total: 1000,
    error_total: 100,
    success_rate_percent: 0.5,
    error_rate_percent: 0.5,
  }
  
  return (
    <div className="mx-auto pl-6 pr-6">
    <div className="mb-6 flex items-center justify-between">
      <h1 className="text-2xl font-bold">Query Analytics</h1>
      <FilterPanel />
    </div>
    <div className="grid gap-6">
      <KeyMetrics 
        queryTypes={queryTypes} 
        averageDuration={averageDuration} 
        queryRate={queryRate} 
      />
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