"use client"

import * as React from "react"
import {
  type ColumnDef,
  flexRender,
  getCoreRowModel,
  useReactTable,
} from "@tanstack/react-table"
import { ArrowUpDown, ChevronLeft, ChevronRight } from "lucide-react"
import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from "@/components/ui/table"
import { Button } from "@/components/ui/button"
import { Select, SelectContent, SelectItem, SelectTrigger, SelectValue } from "@/components/ui/select"
import { MetricTypeTag } from "@/components/metrics-explorer/metric-type-tag"
import { useLocation } from "wouter"
import { MetricMetadata, PagedResult, TableState } from "@/lib/types"

const columns: ColumnDef<MetricMetadata>[] = [
  {
    accessorKey: "name",
    header: ({ column }) => {
      const isSorted = column.getIsSorted()
      return (
        <Button
          variant="ghost"
          style={{ padding: 0 }}
          className="p-0 font-medium text-sm hover:bg-transparent"
          onClick={() => column.toggleSorting(isSorted === "asc")}
        >
          Metric Name
          <ArrowUpDown className="ml-2 h-3 w-3" />
        </Button>
      )
    },
    cell: ({ row }) => {
      return (
        <div className="font-medium max-w-[300px] truncate" title={row.getValue("name")}>
          {row.getValue("name")}
        </div>
      )
    },
  },
  {
    accessorKey: "type",
    header: ({ column }) => {
      const isSorted = column.getIsSorted()
      return (
        <Button
          variant="ghost"
          className="p-0 font-medium text-sm hover:bg-transparent"
          onClick={() => column.toggleSorting(isSorted === "asc")}
        >
          Type
          <ArrowUpDown className="ml-2 h-3 w-3" />
        </Button>
      )
    },
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
]

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

  const table = useReactTable({
    data: metrics?.data || [],
    columns,
    getCoreRowModel: getCoreRowModel(),
    manualPagination: true,
    manualSorting: true,
    pageCount: metrics?.totalPages || 0,
    state: {
      pagination: {
        pageIndex: (tableState.page || 1) - 1,
        pageSize: tableState.pageSize || 10,
      },
      sorting: [{
        id: tableState.sortBy || 'name',
        desc: tableState.sortOrder === 'desc',
      }],
    },
    onPaginationChange: (updater) => {
      if (typeof updater === 'function') {
        const newState = updater(table.getState().pagination)
        onTableStateChange({
          ...tableState,
          page: newState.pageIndex + 1,
          pageSize: newState.pageSize,
        })
      }
    },
    onSortingChange: (updater) => {
      if (typeof updater === 'function') {
        const newState = updater(table.getState().sorting)
        onTableStateChange({
          ...tableState,
          page: tableState.page, // Preserve current page when sorting
          sortBy: newState[0]?.id || 'name',
          sortOrder: newState[0]?.desc ? 'desc' : 'asc',
        })
      }
    },
  })


  return (
    <div className="mt-4 rounded-sm border">
      <Table>
        <TableHeader>
          {table.getHeaderGroups().map((headerGroup) => (
            <TableRow key={headerGroup.id} className="border-b hover:bg-transparent">
              {headerGroup.headers.map((header) => {
                return (
                  <TableHead key={header.id} className="h-10" style={{ width: header.id === 'type' ? '120px' : 'auto' }}>
                    {header.isPlaceholder ? null : flexRender(header.column.columnDef.header, header.getContext())}
                  </TableHead>
                )
              })}
            </TableRow>
          ))}
        </TableHeader>
        <TableBody>
          {table.getRowModel().rows?.length ? (
            table.getRowModel().rows.map((row) => (
              <TableRow
                key={row.id}
                className="cursor-pointer hover:bg-muted/50"
                onClick={() => setLocation(`/metrics/${row.getValue("name")}`)}
              >
                {row.getVisibleCells().map((cell) => (
                  <TableCell key={cell.id} className="py-3">
                    {flexRender(cell.column.columnDef.cell, cell.getContext())}
                  </TableCell>
                ))}
              </TableRow>
            ))
          ) : (
            <TableRow>
              <TableCell colSpan={columns.length} className="h-24 text-center">
                No results.
              </TableCell>
            </TableRow>
          )}
        </TableBody>
      </Table>
      <div className="flex items-center justify-between border-t px-4 py-2">
        <div className="flex items-center gap-4">
          <div className="flex items-center gap-2">
            <span className="text-sm text-muted-foreground">Rows per page</span>
            <Select
              value={`${table.getState().pagination.pageSize}`}
              onValueChange={(value) => {
                table.setPageSize(Number(value))
              }}
            >
              <SelectTrigger className="h-8 w-[70px]">
                <SelectValue placeholder={table.getState().pagination.pageSize} />
              </SelectTrigger>
              <SelectContent side="top">
                {[10, 20, 30, 40, 50].map((pageSize) => (
                  <SelectItem key={pageSize} value={`${pageSize}`}>
                    {pageSize}
                  </SelectItem>
                ))}
              </SelectContent>
            </Select>
          </div>
          <span className="text-sm text-muted-foreground">
            {metrics?.total || 0} metrics
          </span>
        </div>
        <div className="flex items-center gap-2">
          <Button
            variant="outline"
            size="sm"
            className="h-8 w-8 p-0"
            onClick={() => table.previousPage()}
            disabled={!table.getCanPreviousPage()}
          >
            <ChevronLeft className="h-4 w-4" />
          </Button>
          <div className="flex min-w-[100px] items-center justify-center text-sm">
            Page {table.getState().pagination.pageIndex + 1} of {table.getPageCount()}
          </div>
          <Button
            variant="outline"
            size="sm"
            className="h-8 w-8 p-0"
            onClick={() => table.nextPage()}
            disabled={!table.getCanNextPage()}
          >
            <ChevronRight className="h-4 w-4" />
          </Button>
        </div>
      </div>
    </div>
  )
}
