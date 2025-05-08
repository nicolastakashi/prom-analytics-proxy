"use client"

import * as React from "react"
import {
  type ColumnDef,
  type ColumnFiltersState,
  type SortingState,
  flexRender,
  getCoreRowModel,
  useReactTable,
} from "@tanstack/react-table"
import { ArrowUpDown, ChevronLeft, ChevronRight } from "lucide-react"
import { Card } from "@/components/ui/card"
import { Input } from "@/components/ui/input"
import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from "@/components/ui/table"
import { Button } from "@/components/ui/button"
import { Select, SelectContent, SelectItem, SelectTrigger, SelectValue } from "@/components/ui/select"
import { useTable } from "@/contexts/table-context"
import { RecentQuery, PagedResult } from "@/lib/types"
import { formatUTCtoLocal } from "@/lib/utils/date-utils"

interface QueryTableProps {
  data?: PagedResult<RecentQuery>
}

const columns: ColumnDef<RecentQuery>[] = [
  {
    accessorKey: "queryParam",
    maxSize: 200,
    header: ({ column }) => {
      return (
        <div
          className="flex items-center cursor-pointer"
          onClick={() => column.toggleSorting(column.getIsSorted() === "asc")}
        >
          Query
          <ArrowUpDown className="ml-2 h-4 w-4" />
        </div>
      )
    },
    cell: ({ row }) => {
      return (
        <div className="font-medium" title={row.getValue("queryParam")}>
          {row.getValue("queryParam")}
        </div>
      )
    },
  },
  {
    accessorKey: "duration",
    header: ({ column }) => {
      return (
        <div
          className="flex cursor-pointer items-center justify-end"
          onClick={() => column.toggleSorting(column.getIsSorted() === "asc")}
        >
          Duration
          <ArrowUpDown className="ml-2 h-4 w-4" />
        </div>
      )
    },
    cell: ({ row }) => {
      const duration = Number.parseFloat(row.getValue("duration"))
      return <div className="text-right">{duration}ms</div>
    },
  },
  {
    accessorKey: "samples",
    header: ({ column }) => {
      return (
        <div
          className="flex cursor-pointer items-center justify-end"
          onClick={() => column.toggleSorting(column.getIsSorted() === "asc")}
        >
          Samples
          <ArrowUpDown className="ml-2 h-4 w-4" />
        </div>
      )
    },
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
    header: ({ column }) => {
      return (
        <div
          className="flex items-center cursor-pointer"
          onClick={() => column.toggleSorting(column.getIsSorted() === "asc")}
        >
          Timestamp
          <ArrowUpDown className="ml-2 h-4 w-4" />
        </div>
      )
    },
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

export function QueryTable({ data }: QueryTableProps) {
  const { tableState, setTableState } = useTable()
  const [sorting, setSorting] = React.useState<SortingState>([{ id: "timestamp", desc: true }])
  const [columnFilters, setColumnFilters] = React.useState<ColumnFiltersState>([])

  const table = useReactTable({
    data: data?.data || [],
    columns,
    getCoreRowModel: getCoreRowModel(),
    manualPagination: true,
    manualSorting: true,
    manualFiltering: true,
    pageCount: data?.totalPages || 0,
    onSortingChange: (updater) => {
      if (typeof updater === 'function') {
        const newSorting = updater(sorting)
        setSorting(newSorting)
        if (newSorting.length > 0) {
          setTableState({
            ...tableState,
            page: 1, // Reset to first page when sorting changes
            sortBy: newSorting[0].id,
            sortOrder: newSorting[0].desc ? 'desc' : 'asc'
          })
        }
      }
    },
    onColumnFiltersChange: (updater) => {
      if (typeof updater === 'function') {
        const newFilters = updater(columnFilters)
        setColumnFilters(newFilters)
        if (newFilters.length > 0) {
          setTableState({
            ...tableState,
            page: 1, // Reset to first page when filter changes
            filter: newFilters[0].value as string
          })
        } else {
          setTableState({
            ...tableState,
            page: 1,
            filter: ''
          })
        }
      }
    },
    state: {
      sorting,
      columnFilters,
      pagination: {
        pageIndex: tableState.page - 1,
        pageSize: tableState.pageSize,
      },
    },
    onPaginationChange: (updater) => {
      if (typeof updater === 'function') {
        const newState = updater({ pageIndex: tableState.page - 1, pageSize: tableState.pageSize })
        setTableState({
          ...tableState,
          page: newState.pageIndex + 1,
          pageSize: newState.pageSize
        })
      }
    },
  })

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
              value={(table.getColumn("queryParam")?.getFilterValue() as string) ?? ""}
              onChange={(event) => table.getColumn("queryParam")?.setFilterValue(event.target.value)}
              className="sm:max-w-[300px]"
            />
          </div>
          <div className="rounded-md border">
            <Table>
              <TableHeader>
                {table.getHeaderGroups().map((headerGroup) => (
                  <TableRow key={headerGroup.id}>
                    {headerGroup.headers.map((header) => {
                      return (
                        <TableHead key={header.id} className="whitespace-nowrap">
                          {header.isPlaceholder
                            ? null
                            : flexRender(header.column.columnDef.header, header.getContext())}
                        </TableHead>
                      )
                    })}
                  </TableRow>
                ))}
              </TableHeader>
              <TableBody>
                {table.getRowModel().rows?.length ? (
                  table.getRowModel().rows.map((row) => (
                    <TableRow key={row.id} className="cursor-pointer hover:bg-muted/50">
                      {row.getVisibleCells().map((cell) => (
                        <TableCell key={cell.id}>{flexRender(cell.column.columnDef.cell, cell.getContext())}</TableCell>
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
          </div>
          <div className="flex items-center justify-between border-t px-4 py-2">
            <div className="flex items-center gap-4">
              <div className="flex items-center gap-2">
                <span className="text-sm text-muted-foreground">Rows per page</span>
                <Select
                  value={`${tableState.pageSize}`}
                  onValueChange={(value) => {
                    const newSize = Number(value)
                    setTableState({
                      ...tableState,
                      page: 1,
                      pageSize: newSize
                    })
                  }}
                >
                  <SelectTrigger className="h-8 w-[70px]">
                    <SelectValue placeholder={tableState.pageSize} />
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
                {data?.total || 0} queries
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
                Page {tableState.page} of {table.getPageCount()}
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
      </div>
    </Card>
  )
}

