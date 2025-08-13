"use client"

import * as React from "react"
import { type ColumnDef, SortingState } from "@tanstack/react-table"
import { MetricTypeTag } from "@/components/metrics-explorer/metric-type-tag"
import { useLocation } from "wouter"
import { MetricMetadata, PagedResult, TableState } from "@/lib/types"
import { fromUTC } from "@/lib/utils/date-utils"
import { DataTable, DataTableColumnHeader } from "@/components/data-table"
import { ROUTES } from "@/lib/routes"

interface MetricsTableProps {
  metrics?: PagedResult<MetricMetadata>
  searchQuery: string
  tableState: TableState
  onTableStateChange: (state: TableState) => void
}

export function MetricsTable({ 
  metrics,
  searchQuery,
  tableState,
  onTableStateChange,
}: MetricsTableProps) {
  const [, setLocation] = useLocation()
  const prevSearchRef = React.useRef(searchQuery)

  // Update filter when search query changes
  React.useEffect(() => {
    if (prevSearchRef.current !== searchQuery) {
      prevSearchRef.current = searchQuery
      onTableStateChange({
        ...tableState,
        filter: searchQuery,
        page: 1, // Reset to first page only when search changes
      })
    }
  }, [searchQuery, tableState, onTableStateChange])

  const handleMetricClick = (metricName: string) => {
    // Use the route path from ROUTES constant, replacing the parameter with the actual value
    const detailsPath = ROUTES.METRICS_DETAILS.replace(':metric', encodeURIComponent(metricName))
    setLocation(detailsPath)
  }
  
  // Initialize sorting state from tableState
  const sortingState: SortingState = React.useMemo(() => {
    return tableState.sortBy ? [
      { 
        id: tableState.sortBy, 
        desc: tableState.sortOrder === 'desc' 
      }
    ] : []
  }, [tableState.sortBy, tableState.sortOrder]);

  // Handler functions for DataTable callbacks
  const handleSortingChange = (newSorting: SortingState) => {
    if (newSorting.length > 0) {
      onTableStateChange({
        ...tableState,
        sortBy: newSorting[0].id,
        sortOrder: newSorting[0].desc ? 'desc' : 'asc',
      });
    }
  };

  const handleFilterChange = (value: string) => {
    onTableStateChange({
      ...tableState,
      filter: value,
      page: 1, // Reset to first page on filter change
    });
  };

  const handlePaginationChange = (page: number) => {
    onTableStateChange({
      ...tableState,
      page,
    });
  };

  const columns: ColumnDef<MetricMetadata>[] = [
    {
      accessorKey: "name",
      header: ({ column }) => (
        <DataTableColumnHeader column={column} title="Metric Name" />
      ),
      cell: ({ row }) => {
        return (
          <div 
            className="font-medium max-w-[300px] truncate cursor-pointer hover:text-blue-500" 
            title={row.getValue("name")}
            onClick={(e) => {
              e.stopPropagation(); // Prevent row click from triggering twice
              handleMetricClick(row.getValue("name"));
            }}
          >
            {row.getValue("name")}
          </div>
        )
      },
    },
    {
      accessorKey: "type",
      header: ({ column }) => (
        <DataTableColumnHeader column={column} title="Type" />
      ),
      cell: ({ row }) => <MetricTypeTag type={row.getValue("type")} />,
    },
    {
      accessorKey: "help",
      header: "Description",
      cell: ({ row }) => (
        <div className="text-sm text-muted-foreground max-w-[500px] truncate" title={row.getValue("help")}>
          {row.getValue("help")}
        </div>
      ),
    },
    {
      accessorKey: "alertCount",
      header: ({ column }) => (
        <DataTableColumnHeader column={column} title="Alerts" />
      ),
      cell: ({ row }) => <span className="tabular-nums">{row.getValue("alertCount") ?? 0}</span>,
      enableSorting: false,
    },
    {
      accessorKey: "recordCount",
      header: ({ column }) => (
        <DataTableColumnHeader column={column} title="Records" />
      ),
      cell: ({ row }) => <span className="tabular-nums">{row.getValue("recordCount") ?? 0}</span>,
      enableSorting: false,
    },
    {
      accessorKey: "dashboardCount",
      header: ({ column }) => (
        <DataTableColumnHeader column={column} title="Dashboards" />
      ),
      cell: ({ row }) => <span className="tabular-nums">{row.getValue("dashboardCount") ?? 0}</span>,
      enableSorting: false,
    },
    {
      accessorKey: "queryCount",
      header: ({ column }) => (
        <DataTableColumnHeader column={column} title="Queries (30d)" />
      ),
      cell: ({ row }) => {
        const count = row.getValue<number>("queryCount") ?? 0
        const last = row.original.lastQueriedAt
        return (
          <div className="flex items-center gap-2">
            <span className="tabular-nums">{count}</span>
            {last && (
              <span className="text-xs text-muted-foreground" title={`Last queried at ${fromUTC(last).toLocaleString()}`}>
                •
              </span>
            )}
          </div>
        )
      },
      enableSorting: false,
    },
  ]

  return (
    <div className="mt-4">
      <DataTable<MetricMetadata>
        data={metrics?.data || []}
        columns={columns}
        pagination={true}
        pageSize={tableState.pageSize}
        className="w-full"
        serverSide={true}
        sortingState={sortingState}
        filterValue={tableState.filter}
        currentPage={tableState.page}
        totalPages={metrics?.totalPages || 1}
        onSortingChange={handleSortingChange}
        onFilterChange={handleFilterChange}
        onPaginationChange={handlePaginationChange}
        onRowClick={(row) => handleMetricClick(row.name)}
      />
    </div>
  )
}
