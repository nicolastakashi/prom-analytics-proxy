import { useState } from "react";
import { MetricsExplorerHeader } from "@/components/metrics-explorer/metrics-explorer-header";
import { MetricsTable } from "@/components/metrics-explorer/metrics-table";
import { useSeriesMetadataTable } from "./use-metrics-data";
import { useTableState } from "@/hooks/use-table-state";
import { LoadingState } from "./loading";
import { useDebounce } from "@/hooks/use-debounce";

export default function MetricsExplorer() {
  const [searchQuery, setSearchQuery] = useState("");
  const [typeFilter, setTypeFilter] = useState("all");
  const [usageFilter, setUsageFilter] = useState<"all" | "used" | "unused">(
    "all",
  );
  const [producerFilter, setProducerFilter] = useState<string>("");
  const tableState = useTableState({
    defaultSortBy: "queryCount",
    defaultSortOrder: "desc",
  });

  const debouncedSearchQuery = useDebounce(searchQuery, 750);

  const { data, isLoading, error } = useSeriesMetadataTable(
    {
      page: tableState.page,
      pageSize: tableState.pageSize,
      sortBy: tableState.sortBy,
      sortOrder: tableState.sortOrder,
      filter: tableState.filter,
      type: typeFilter,
    },
    debouncedSearchQuery,
    usageFilter,
    producerFilter,
  );

  if (isLoading) {
    return <LoadingState />;
  }

  if (error) {
    return <div>Error: {error.message}</div>;
  }

  const handleTypeFilterChange = (value: string) => {
    setTypeFilter(value);
    tableState.setPage(1);
  };

  return (
    <div className="p-4">
      <MetricsExplorerHeader
        searchQuery={searchQuery}
        onSearchChange={setSearchQuery}
        typeFilter={typeFilter}
        onTypeFilterChange={handleTypeFilterChange}
        usageFilter={usageFilter}
        onUsageFilterChange={setUsageFilter}
        producers={data.producers}
        producerFilter={producerFilter}
        onProducerFilterChange={setProducerFilter}
      />
      <MetricsTable
        metrics={data.metrics}
        searchQuery={debouncedSearchQuery}
        tableState={tableState}
      />
    </div>
  );
}
