"use client"

import * as React from "react"
import { type ColumnDef, SortingState } from "@tanstack/react-table"
import { Card } from "@/components/ui/card"
import { Input } from "@/components/ui/input"
import { useTable } from "@/contexts/table-context"
import { RecentQuery, PagedResult } from "@/lib/types"
import { formatUTCtoLocal } from "@/lib/utils/date-utils"
import { DataTable, DataTableColumnHeader } from "@/components/data-table"
import { ArrowUpDown } from "lucide-react"
import { useQuery } from "@tanstack/react-query"
import { useDateRange } from "@/contexts/date-range-context"
import { getRecentQueries } from "@/api/queries"
import { Skeleton } from "@/components/ui/skeleton"

// Define our extended column type with maxWidth
type ExtendedColumnDef<TData, TValue = unknown> = ColumnDef<TData, TValue> & { maxWidth?: string | number };

const columns: ExtendedColumnDef<RecentQuery>[] = [
  {
    accessorKey: "queryParam",
    maxWidth: 600,
    header: ({ column }) => (
      <DataTableColumnHeader column={column} title="Query" />
    ),
    cell: ({ row }) => String(row.getValue("queryParam")),
  },
  {
    accessorKey: "duration",
    header: ({ column }) => (
      <DataTableColumnHeader column={column} title="Duration" />
    ),
    cell: ({ row }) => {
      const duration = Number.parseFloat(row.getValue("duration"))
      return <div className="text-right">{duration}ms</div>
    },
  },
  {
    accessorKey: "samples",
    header: ({ column }) => (
      <DataTableColumnHeader column={column} title="Samples" />
    ),
    cell: ({ row }) => {
      const samples = Number.parseInt(row.getValue("samples"))
      return <div className="text-right">{samples.toLocaleString()}</div>
    },
  },
  {
    accessorKey: "status",
    header: ({ column }) => {
      return (
        <div
          className="flex items-center cursor-pointer"
          onClick={() => column.toggleSorting(column.getIsSorted() === "asc")}
        >
          Status
          <ArrowUpDown className="ml-2 h-4 w-4" />
        </div>
      )
    },
    cell: ({ row }) => {
      const status = row.getValue("status") as number
      return (
        <div
          className={`inline-flex rounded-full px-2 py-1 text-xs font-medium ${
            status === 200 ? "bg-green-500/20 text-green-500" : "bg-red-500/20 text-red-500"
          }`}
        >
          {status}
        </div>
      )
    },
  },
  {
    accessorKey: "timestamp",
    header: ({ column }) => (
      <DataTableColumnHeader column={column} title="Timestamp" />
    ),
    cell: ({ row }) => {
      const timestamp = row.getValue("timestamp") as string
      return (
        <div className="font-medium whitespace-nowrap">
          {formatUTCtoLocal(timestamp, "MMM d, yyyy HH:mm:ss")}
        </div>
      )
    },
  },
]

export function QueryTable() {
  const { tableState, setTableState } = useTable()
  const [sorting, setSorting] = React.useState<SortingState>([{ id: "timestamp", desc: true }])

  // Initialize sorting state from tableState
  React.useEffect(() => {
    if (tableState.sortBy) {
      setSorting([{
        id: tableState.sortBy,
        desc: tableState.sortOrder === 'desc'
      }])
    }
  }, [tableState.sortBy, tableState.sortOrder])

  const handleSortingChange = (newSorting: SortingState) => {
    if (newSorting.length > 0) {
      setTableState({
        ...tableState,
        page: 1, // Reset to first page when sorting changes
        sortBy: newSorting[0].id,
        sortOrder: newSorting[0].desc ? 'desc' : 'asc'
      })
    }
  }

  const handleFilterChange = (value: string) => {
    setTableState({
      ...tableState,
      page: 1, // Reset to first page on filter change
      filter: value
    })
  }

  const handlePaginationChange = (page: number) => {
    setTableState({
      ...tableState,
      page
    })
  }

  const { dateRange } = useDateRange()
  const fromISO = dateRange?.from?.toISOString()
  const toISO = dateRange?.to?.toISOString()

  const { data, isLoading } = useQuery<PagedResult<RecentQuery>>({
    queryKey: ["recentQueries", fromISO, toISO, tableState],
    queryFn: () => getRecentQueries(
      fromISO,
      toISO,
      tableState.page,
      tableState.pageSize,
      tableState.sortBy,
      tableState.sortOrder,
      tableState.filter
    ),
    enabled: Boolean(fromISO && toISO),
  })

  if (isLoading || !data) {
    return <Skeleton className="h-[500px] w-full" />
  }

  return (
    <Card>
      <div className="p-6">
        <div className="mb-6">
          <h1 className="text-2xl font-bold">Queries</h1>
          <p className="text-sm text-muted-foreground">Browse and analyze query patterns in your queries</p>
        </div>
        <div className="space-y-4">
          <div className="flex flex-col gap-4 sm:flex-row sm:items-center">
            <Input
              placeholder="Search queries..."
              value={tableState.filter || ""}
              onChange={(event) => handleFilterChange(event.target.value)}
              className="sm:max-w-[300px]"
            />
          </div>
          <DataTable<RecentQuery>
            data={data?.data || []}
            columns={columns}
            pagination={true}
            pageSize={tableState.pageSize}
            className="w-full"
            serverSide={true}
            sortingState={sorting}
            filterValue={tableState.filter}
            currentPage={tableState.page}
            totalPages={data?.totalPages || 1}
            onSortingChange={handleSortingChange}
            onFilterChange={handleFilterChange}
            onPaginationChange={handlePaginationChange}
          />
        </div>
      </div>
    </Card>
  )
}

