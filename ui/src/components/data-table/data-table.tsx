import React, { useState, useEffect } from "react";
import {
  flexRender,
  getCoreRowModel,
  useReactTable,
  getPaginationRowModel,
  getSortedRowModel,
  getFilteredRowModel,
  SortingState,
  OnChangeFn,
  ColumnDef,
} from "@tanstack/react-table";
import {
  Table,
  TableBody,
  TableCell,
  TableHead,
  TableHeader,
  TableRow,
} from "@/components/ui/table";
import { Tooltip, TooltipContent, TooltipProvider, TooltipTrigger } from "@/components/ui/tooltip";
import { DataTableFilter } from "./data-table-filter";
import { DataTablePagination } from "./data-table-pagination";
import { DataTableProps } from "./types";

export function DataTable<TData>({
  data,
  columns,
  searchColumn,
  pagination = true,
  pageSize = 10,
  onRowClick,
  className,
  // Server-side props
  serverSide = false,
  sortingState: externalSortingState,
  filterValue: externalFilterValue,
  totalPages: externalTotalPages,
  currentPage: externalCurrentPage,
  onSortingChange,
  onFilterChange,
  onPaginationChange,
}: DataTableProps<TData>) {
  // Local state for client-side operations
  const [sorting, setSorting] = useState<SortingState>(externalSortingState || []);
  const [globalFilter, setGlobalFilter] = useState(externalFilterValue || "");
  const [pageIndex, setPageIndex] = useState(externalCurrentPage ? externalCurrentPage - 1 : 0);
  const [currentPageSize] = useState(pageSize);

  // Use external state if in server-side mode
  useEffect(() => {
    if (serverSide && externalSortingState) {
      setSorting(externalSortingState);
    }
  }, [serverSide, externalSortingState]);

  useEffect(() => {
    if (serverSide && externalFilterValue !== undefined) {
      setGlobalFilter(externalFilterValue);
    }
  }, [serverSide, externalFilterValue]);

  useEffect(() => {
    if (serverSide && externalCurrentPage !== undefined) {
      setPageIndex(externalCurrentPage - 1);
    }
  }, [serverSide, externalCurrentPage]);

  // Define handlers for state changes
  const handleSortingChange: OnChangeFn<SortingState> = (updaterOrValue) => {
    // Handle both function updater and direct value forms
    const newSorting = typeof updaterOrValue === 'function'
      ? updaterOrValue(sorting)
      : updaterOrValue;
    
    if (serverSide) {
      if (onSortingChange) {
        onSortingChange(newSorting);
      }
    } else {
      setSorting(newSorting);
    }
  };

  const handleFilterChange = (value: string) => {
    if (serverSide) {
      if (onFilterChange) {
        onFilterChange(value);
      }
    } else {
      setGlobalFilter(value);
    }
  };

  const handlePageChange = (page: number) => {
    if (serverSide) {
      if (onPaginationChange) {
        onPaginationChange(page);
      }
    } else {
      setPageIndex(page - 1);
    }
  };

  // Reset to first page when data changes (for client-side mode)
  useEffect(() => {
    if (!serverSide) {
      setPageIndex(0);
    }
  }, [data, serverSide]);

  const table = useReactTable({
    data,
    columns,
    getCoreRowModel: getCoreRowModel(),
    getPaginationRowModel: pagination && !serverSide ? getPaginationRowModel() : undefined,
    getSortedRowModel: !serverSide ? getSortedRowModel() : undefined,
    getFilteredRowModel: !serverSide ? getFilteredRowModel() : undefined,
    onSortingChange: handleSortingChange,
    onGlobalFilterChange: handleFilterChange,
    state: {
      sorting,
      globalFilter,
      pagination: {
        pageIndex,
        pageSize: currentPageSize,
      },
    },
    // Disable table features if using server-side mode
    manualSorting: serverSide,
    manualFiltering: serverSide,
    manualPagination: serverSide,
  });

  // Calculate total pages for client-side pagination
  const totalPages = serverSide 
    ? externalTotalPages || 1 
    : Math.ceil(table.getFilteredRowModel().rows.length / currentPageSize);

  return (
    <div className={className}>
      {searchColumn && (
        <DataTableFilter
          value={globalFilter}
          onChange={handleFilterChange}
          placeholder={`Search ${String(searchColumn)}...`}
        />
      )}

      <div className="rounded-md border">
        <Table>
          <TableHeader>
            {table.getHeaderGroups().map((headerGroup) => (
              <TableRow key={headerGroup.id}>
                {headerGroup.headers.map((header) => (
                  <TableHead key={header.id}>
                    {header.isPlaceholder
                      ? null
                      : flexRender(
                          header.column.columnDef.header,
                          header.getContext()
                        )}
                  </TableHead>
                ))}
              </TableRow>
            ))}
          </TableHeader>
          <TableBody>
            {table.getRowModel().rows?.length ? (
              table.getRowModel().rows.map((row) => (
                <TableRow
                  key={row.id}
                  data-state={row.getIsSelected() && "selected"}
                  onClick={onRowClick ? () => onRowClick(row.original) : undefined}
                  className={onRowClick ? "cursor-pointer" : ""}
                >
                  {row.getVisibleCells().map((cell) => {
                    // Get maxWidth from column def if it exists
                    const columnDef = cell.column.columnDef as ColumnDef<TData, unknown> & { maxWidth?: string | number };
                    const maxWidth = columnDef.maxWidth;

                    // Prepare cell content
                    const cellContent = flexRender(
                      cell.column.columnDef.cell,
                      cell.getContext()
                    );

                    return (
                      <TableCell key={cell.id}>
                        {maxWidth ? (
                          <TooltipProvider>
                            <Tooltip>
                              <TooltipTrigger asChild>
                                <div
                                  style={{
                                    maxWidth: typeof maxWidth === 'number' ? `${maxWidth}px` : maxWidth,
                                    overflow: "hidden",
                                    textOverflow: "ellipsis",
                                    whiteSpace: "nowrap"
                                  }}
                                >
                                  {cellContent}
                                </div>
                              </TooltipTrigger>
                              <TooltipContent side="top" align="start" className="max-w-lg bg-gray-900 text-white p-2 text-sm rounded-md shadow-lg">
                                <div className="break-all font-mono">{cellContent}</div>
                              </TooltipContent>
                            </Tooltip>
                          </TooltipProvider>
                        ) : (
                          cellContent
                        )}
                      </TableCell>
                    );
                  })}
                </TableRow>
              ))
            ) : (
              <TableRow>
                <TableCell
                  colSpan={columns.length}
                  className="h-24 text-center text-muted-foreground"
                >
                  No results found
                </TableCell>
              </TableRow>
            )}
          </TableBody>
        </Table>
      </div>

      {pagination && (
        <DataTablePagination
          currentPage={serverSide ? externalCurrentPage || 1 : pageIndex + 1}
          totalPages={totalPages}
          onPageChange={handlePageChange}
        />
      )}
    </div>
  );
} 