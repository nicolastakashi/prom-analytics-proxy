"use client";

import * as React from "react";
import { type ColumnDef, SortingState } from "@tanstack/react-table";
import { MetricTypeTag } from "@/components/metrics-explorer/metric-type-tag";
import { useLocation } from "wouter";
import { MetricMetadata, PagedResult } from "@/lib/types";
import { UseTableStateResult } from "@/hooks/use-table-state";
import { fromUTC } from "@/lib/utils/date-utils";
import { DataTable, DataTableColumnHeader } from "@/components/data-table";
import { ROUTES } from "@/lib/routes";

interface MetricsTableProps {
  metrics?: PagedResult<MetricMetadata>;
  searchQuery: string;
  tableState: UseTableStateResult;
}

export function MetricsTable({
  metrics,
  searchQuery,
  tableState,
}: MetricsTableProps) {
  const [, setLocation] = useLocation();
  const prevSearchRef = React.useRef(searchQuery);

  // Sync external search query into table state (resets page via setFilter)
  React.useEffect(() => {
    if (prevSearchRef.current !== searchQuery) {
      prevSearchRef.current = searchQuery;
      tableState.setFilter(searchQuery);
    }
  }, [searchQuery, tableState]);

  const handleMetricClick = (metricName: string) => {
    // Use the route path from ROUTES constant, replacing the parameter with the actual value
    const detailsPath = ROUTES.METRICS_DETAILS.replace(
      ":metric",
      encodeURIComponent(metricName),
    );
    setLocation(detailsPath);
  };

  const sortingState = tableState.sorting;

  const handleSortingChange = (newSorting: SortingState) => {
    tableState.setSorting(newSorting);
  };

  const handleFilterChange = (value: string) => {
    tableState.setFilter(value);
  };

  const handlePaginationChange = (page: number) => {
    tableState.setPage(page);
  };

  const columns: ColumnDef<MetricMetadata>[] = [
    {
      accessorKey: "name",
      size: 200,
      header: ({ column }) => (
        <DataTableColumnHeader column={column} title="Metric Name" />
      ),
      cell: ({ row }) => {
        return (
          <div
            className="font-medium max-w-[200px] truncate cursor-pointer hover:text-blue-500"
            title={row.getValue("name")}
            onClick={(e) => {
              e.stopPropagation();
              handleMetricClick(row.getValue("name"));
            }}
          >
            {row.getValue("name")}
          </div>
        );
      },
    },
    {
      accessorKey: "type",
      size: 100,
      header: ({ column }) => (
        <DataTableColumnHeader column={column} title="Type" />
      ),
      cell: ({ row }) => <MetricTypeTag type={row.getValue("type")} />,
    },
    {
      accessorKey: "help",
      size: 250,
      header: "Description",
      cell: ({ row }) => (
        <div
          className="text-sm text-muted-foreground truncate"
          title={row.getValue("help")}
        >
          {row.getValue("help")}
        </div>
      ),
    },
    {
      accessorKey: "alertCount",
      size: 80,
      header: ({ column }) => (
        <DataTableColumnHeader column={column} title="Alerts" />
      ),
      cell: ({ row }) => (
        <span className="tabular-nums">{row.getValue("alertCount") ?? 0}</span>
      ),
    },
    {
      accessorKey: "recordCount",
      size: 90,
      header: ({ column }) => (
        <DataTableColumnHeader column={column} title="Records" />
      ),
      cell: ({ row }) => (
        <span className="tabular-nums">{row.getValue("recordCount") ?? 0}</span>
      ),
    },
    {
      accessorKey: "dashboardCount",
      size: 110,
      header: ({ column }) => (
        <DataTableColumnHeader column={column} title="Dashboards" />
      ),
      cell: ({ row }) => (
        <span className="tabular-nums">
          {row.getValue("dashboardCount") ?? 0}
        </span>
      ),
    },
    {
      accessorKey: "queryCount",
      size: 120,
      header: ({ column }) => (
        <DataTableColumnHeader column={column} title="Queries (30d)" />
      ),
      cell: ({ row }) => {
        const count = row.getValue<number>("queryCount") ?? 0;
        const last = row.original.lastQueriedAt;
        return (
          <div className="flex items-center gap-2">
            <span className="tabular-nums">{count}</span>
            {last && (
              <span
                className="text-xs text-muted-foreground"
                title={`Last queried at ${fromUTC(last).toLocaleString()}`}
              >
                •
              </span>
            )}
          </div>
        );
      },
    },
  ];

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
  );
}
