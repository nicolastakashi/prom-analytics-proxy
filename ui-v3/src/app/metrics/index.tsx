import { useState } from "react"
import { MetricsExplorerHeader } from "@/components/metrics-explorer/metrics-explorer-header"
import { MetricsTable } from "@/components/metrics-explorer/metrics-table"
import { useSeriesMetadataTable } from "./use-metrics-data"
import { TableState } from "@/lib/types"
import { LoadingState } from "./loading"
import { useDebounce } from "@/hooks/use-debounce"

export default function MetricsExplorer() {
  const [searchQuery, setSearchQuery] = useState("")
  const [typeFilter, setTypeFilter] = useState("all")
  const [tableState, setTableState] = useState<TableState>({
    page: 1,
    pageSize: 10,
    sortBy: "name",
    sortOrder: "asc",
    filter: "",
    type: "all",
  })

  // Increase debounce delay to 750ms for better performance
  const debouncedSearchQuery = useDebounce(searchQuery, 750)

  const { data, isLoading, error } = useSeriesMetadataTable(tableState, debouncedSearchQuery)

  if (isLoading) {
    return <LoadingState />
  }

  if (error) {
    return <div>Error: {error.message}</div>
  }

  const handleTypeFilterChange = (value: string) => {
    setTypeFilter(value)
    setTableState(prev => ({
      ...prev,
      page: 1, // Reset to first page when changing type
      type: value,
    }))
  }

  return (
    <div className="p-4">
      <MetricsExplorerHeader 
        searchQuery={searchQuery}
        onSearchChange={setSearchQuery}
        typeFilter={typeFilter}
        onTypeFilterChange={handleTypeFilterChange}
      />
      <MetricsTable 
        metrics={data.metrics}
        searchQuery={debouncedSearchQuery}
        tableState={tableState}
        onTableStateChange={setTableState}
      />
    </div>
  )
}
