import { useState } from "react"
import { useQuery } from "@tanstack/react-query"
import { Input } from "@/components/ui/input"
import { DataTable, DataTableColumnHeader } from "@/components/data-table"
import type { ColumnDef, SortingState } from "@tanstack/react-table"
import { useDateRange } from "@/contexts/date-range-context"
import { useDebounce } from "@/hooks/use-debounce"
import { getQueryExpressions } from "@/api/queries"
import { LoadingState } from "./loading"
import type { PagedResult, QueryExpression, TableState } from "@/lib/types"

// Extend ColumnDef to support maxWidth so DataTable can apply ellipsis + tooltip
type ExtendedColumnDef<TData, TValue = unknown> = ColumnDef<TData, TValue> & { maxWidth?: string | number }

const columns: ExtendedColumnDef<QueryExpression>[] = [
  {
    accessorKey: "query",
    maxWidth: 600,
    header: ({ column }) => (
      <DataTableColumnHeader column={column} title="Query" />
    ),
    cell: ({ row }) => String(row.getValue("query")),
  },
  {
    accessorKey: "executions",
    header: ({ column }) => (
      <DataTableColumnHeader column={column} title="Executions" />
    ),
    cell: ({ row }) => {
      const value = Number(row.getValue("executions"))
      return <div className="text-right">{value.toLocaleString()}</div>
    },
  },
  {
    accessorKey: "avgDuration",
    header: ({ column }) => (
      <DataTableColumnHeader column={column} title="Avg Duration" />
    ),
    cell: ({ row }) => {
      const value = Number(row.getValue("avgDuration"))
      return <div className="text-right">{value}ms</div>
    },
  },
  {
    accessorKey: "errorRatePercent",
    header: ({ column }) => (
      <DataTableColumnHeader column={column} title="Error Rate %" />
    ),
    cell: ({ row }) => {
      const value = Number(row.getValue("errorRatePercent"))
      return <div className="text-right">{value}%</div>
    },
  },
  {
    accessorKey: "peakSamples",
    header: ({ column }) => (
      <DataTableColumnHeader column={column} title="Peak Samples" />
    ),
    cell: ({ row }) => {
      const value = Number(row.getValue("peakSamples"))
      return <div className="text-right">{value.toLocaleString()}</div>
    },
  },
]

export default function QueriesPage() {
  const { dateRange } = useDateRange()
  const fromISO = dateRange?.from?.toISOString()
  const toISO = dateRange?.to?.toISOString()

  const [searchQuery, setSearchQuery] = useState("")
  const debouncedSearch = useDebounce(searchQuery, 750)
  const [tableState, setTableState] = useState<TableState>({
    page: 1,
    pageSize: 10,
    sortBy: "executions",
    sortOrder: "desc",
    filter: "",
    type: "all",
  })
  const [sorting, setSorting] = useState<SortingState>([
    { id: tableState.sortBy, desc: tableState.sortOrder === "desc" },
  ])

  const handleSortingChange = (newSorting: SortingState) => {
    setSorting(newSorting)
    if (newSorting.length > 0) {
      setTableState(prev => ({
        ...prev,
        page: 1,
        sortBy: newSorting[0].id,
        sortOrder: newSorting[0].desc ? "desc" : "asc",
      }))
    }
  }

  const handleFilterChange = (value: string) => {
    setSearchQuery(value)
    setTableState(prev => ({ ...prev, page: 1, filter: value }))
  }

  const handlePaginationChange = (page: number) => {
    setTableState(prev => ({ ...prev, page }))
  }

  const { data, isLoading } = useQuery<PagedResult<QueryExpression>>({
    queryKey: ["queryExpressions", fromISO, toISO, tableState.page, tableState.pageSize, tableState.sortBy, tableState.sortOrder, debouncedSearch],
    queryFn: () => getQueryExpressions(
      fromISO,
      toISO,
      tableState.page,
      tableState.pageSize,
      tableState.sortBy,
      tableState.sortOrder,
      debouncedSearch,
    ),
    enabled: Boolean(fromISO && toISO),
  })

  if (isLoading) {
    return <LoadingState />
  }

  return (
    <div className="p-4">
      <div className="mb-4">
        <h1 className="text-2xl font-bold">Queries</h1>
        <p className="text-sm text-muted-foreground">Aggregated view of query fingerprints with executions, latency, errors, and peak samples.</p>
      </div>
      <div className="space-y-4">
        <div className="flex flex-col gap-4 sm:flex-row sm:items-center">
          <Input
            placeholder="Search query expression..."
            value={searchQuery}
            onChange={(e) => handleFilterChange(e.target.value)}
            className="sm:max-w-[300px]"
          />
        </div>
        <DataTable<QueryExpression>
          data={data?.data || []}
          columns={columns}
          pagination={true}
          pageSize={tableState.pageSize}
          className="w-full"
          serverSide={true}
          sortingState={sorting}
          filterValue={debouncedSearch}
          currentPage={tableState.page}
          totalPages={data?.totalPages || 1}
          onSortingChange={handleSortingChange}
          onFilterChange={handleFilterChange}
          onPaginationChange={handlePaginationChange}
        />
      </div>
    </div>
  )
}


