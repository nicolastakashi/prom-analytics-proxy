import React from "react";
import { useMetricUsage, useSerieExpressions } from "@/app/metrics/use-metrics-data";
import { Tabs, TabsContent, TabsList, TabsTrigger } from "@/components/ui/tabs";
import { Card, CardHeader, CardTitle, CardContent } from "@/components/ui/card";
import { Skeleton } from "@/components/ui/skeleton";
import { DataTable, DataTableColumnHeader } from "@/components/data-table";
import { ColumnDef, SortingState } from "@tanstack/react-table";
import { DateRange } from "@/lib/types";

// Define our extended column type with maxWidth
type ExtendedColumnDef<TData, TValue = unknown> = ColumnDef<TData, TValue> & { maxWidth?: string | number };

interface MetricUsageProps {
  metricName: string;
  dateRange?: DateRange | undefined;
}

// Define interface for expression data items
interface ExpressionDataItem {
  query: string;
  avgDuration?: number;
  maxPeakSamples?: number;
  avgPeakySamples?: number;
}

// Define interface for all types of metric usage items
interface MetricUsageItem {
  id?: string;
  name: string;
  url?: string;
  groupName?: string;
  expression?: string;
}

// Define a generic type for tab state
interface TabState {
  page: number;
  filter: string;
  sorting: SortingState;
  sortBy?: string;
  sortOrder?: 'asc' | 'desc';
}

// Custom hook for managing tab state
function useTabState(initialPage = 1, initialSortBy = 'avgDuration', initialSortOrder: 'asc' | 'desc' = 'desc') {
  const [state, setState] = React.useState<TabState>({
    page: initialPage,
    filter: '',
    sorting: initialSortBy ? [{ id: initialSortBy, desc: initialSortOrder === 'desc' }] : [],
    sortBy: initialSortBy,
    sortOrder: initialSortOrder,
  });

  const setPage = (page: number) => {
    setState(prev => ({ ...prev, page }));
  };

  const setFilter = (filter: string) => {
    setState(prev => ({ ...prev, filter, page: 1 })); // Reset to first page on filter change
  };

  const setSorting = (newSorting: SortingState) => {
    if (newSorting.length > 0) {
      const column = newSorting[0].id;
      const direction = newSorting[0].desc ? 'desc' : 'asc';
      
      setState(prev => ({
        ...prev,
        sorting: newSorting,
        sortBy: column,
        sortOrder: direction,
      }));
    } else {
      setState(prev => ({
        ...prev,
        sorting: newSorting,
      }));
    }
  };

  return {
    ...state,
    setPage,
    setFilter,
    setSorting,
  };
}

// Define column configurations
const getQueriesColumns = (): ExtendedColumnDef<ExpressionDataItem, unknown>[] => [
  {
    accessorKey: "query",
    maxWidth: 600,
    header: ({ column }) => (
      <DataTableColumnHeader column={column} title="Query" />
    ),
    cell: ({ row }) => String(row.getValue("query")),
  },
  {
    accessorKey: "avgDuration",
    header: ({ column }) => (
      <DataTableColumnHeader column={column} title="Avg Duration" />
    ),
    cell: ({ row }) => {
      const value = row.getValue("avgDuration") as number;
      return <span>{value?.toFixed(2) ?? 'N/A'}ms</span>;
    },
  },
  {
    accessorKey: "maxPeakSamples",
    header: ({ column }) => (
      <DataTableColumnHeader column={column} title="Peak Samples" />
    ),
    cell: ({ row }) => {
      const value = row.getValue("maxPeakSamples") as number;
      return <span>{value?.toLocaleString() ?? 'N/A'}</span>;
    },
  },
  {
    accessorKey: "avgPeakySamples",
    header: ({ column }) => (
      <DataTableColumnHeader column={column} title="Avg Samples" />
    ),
    cell: ({ row }) => {
      const value = row.getValue("avgPeakySamples") as number;
      return <span>{value?.toFixed(2) ?? 'N/A'}</span>;
    },
  },
];

const getAlertsColumns = (): ExtendedColumnDef<MetricUsageItem, unknown>[] => [
  {
    accessorKey: "name",
    header: ({ column }) => (
      <DataTableColumnHeader column={column} title="Name" />
    ),
  },
  {
    accessorKey: "expression",
    maxWidth: 600,
    header: ({ column }) => (
      <DataTableColumnHeader column={column} title="Expression" />
    ),
    cell: ({ row }) => String(row.getValue("expression")),
  },
];

const getRecordingColumns = (): ExtendedColumnDef<MetricUsageItem, unknown>[] => [
  {
    accessorKey: "name",
    header: ({ column }) => (
      <DataTableColumnHeader column={column} title="Name" />
    ),
  },
  {
    accessorKey: "expression",
    maxWidth: 600,
    header: ({ column }) => (
      <DataTableColumnHeader column={column} title="Expression" />
    ),
    cell: ({ row }) => String(row.getValue("expression")),
  },
];

const getDashboardColumns = (): ExtendedColumnDef<MetricUsageItem, unknown>[] => [
  {
    accessorKey: "title",
    header: ({ column }) => (
      <DataTableColumnHeader column={column} title="Name" />
    ),
  },
  {
    accessorKey: "url",
    header: ({ column }) => (
      <DataTableColumnHeader column={column} title="URL" />
    ),
    cell: ({ row }) => {
      const url = row.getValue("url") as string;
      return url ? (
        <a href={url} target="_blank" rel="noopener noreferrer" className="text-blue-500 hover:underline">
          {url}
        </a>
      ) : 'N/A';
    },
  },
];

// Generic tab content component
interface TabContentProps<T> {
  isLoading: boolean;
  error: unknown;
  data: { data: T[]; totalPages: number } | undefined;
  columns: ColumnDef<T, unknown>[];
  state: TabState;
  searchColumn: string;
  pageSize: number;
  onSortingChange: (sorting: SortingState) => void;
  onFilterChange: (filter: string) => void;
  onPaginationChange: (page: number) => void;
}

// DataTable doesn't enforce the searchColumn type at runtime, so we'll use this workaround
// eslint-disable-next-line @typescript-eslint/no-explicit-any
function TabContent<T>({
  isLoading,
  error,
  data,
  columns,
  state,
  searchColumn,
  pageSize,
  onSortingChange,
  onFilterChange,
  onPaginationChange
}: TabContentProps<T>) {
  if (isLoading) {
    return (
      <Card>
        <CardHeader>
          <CardTitle>
            <Skeleton className="h-4 w-24" />
          </CardTitle>
        </CardHeader>
        <CardContent>
          <Skeleton className="h-[300px] w-full" />
        </CardContent>
      </Card>
    );
  }
  
  if (error) {
    return <div className="text-center py-4 text-red-500">Error loading data</div>;
  }
  
  return (
    <DataTable
      data={data?.data || []}
      columns={columns}
      // Type assertion needed because searchColumn expects keyof T
      // eslint-disable-next-line @typescript-eslint/no-explicit-any
      searchColumn={searchColumn as any}
      pagination={true}
      pageSize={pageSize}
      className="w-full"
      serverSide={true}
      sortingState={state.sorting}
      filterValue={state.filter}
      currentPage={state.page}
      totalPages={data?.totalPages || 1}
      onSortingChange={onSortingChange}
      onFilterChange={onFilterChange}
      onPaginationChange={onPaginationChange}
    />
  );
}

export default function MetricUsage({ metricName, dateRange }: MetricUsageProps) {
  const pageSize = 10;
  
  // Initialize tab states using the custom hook
  const queriesState = useTabState(1, 'avgDuration', 'desc');
  const alertsState = useTabState(1);
  const recordingsState = useTabState(1);
  const dashboardsState = useTabState(1);

  // Fetch data for queries tab
  const {
    data: expressionsData,
    isLoading: isExpressionsLoading,
    error: expressionsError
  } = useSerieExpressions(metricName, {
    page: queriesState.page,
    pageSize,
    sortBy: queriesState.sortBy || 'avgDuration',
    sortOrder: queriesState.sortOrder || 'desc',
    filter: queriesState.filter,
    type: 'all',
  }, dateRange && dateRange.from ? dateRange : undefined);

  // Fetch data for dashboards tab
  const {
    data: dashboardData,
    isLoading: isDashboardLoading,
    error: dashboardError
  } = useMetricUsage(metricName, "dashboard", dashboardsState.page, pageSize, dateRange?.from, dateRange?.to);

  // Fetch data for alerts tab
  const {
    data: alertData,
    isLoading: isAlertLoading,
    error: alertError
  } = useMetricUsage(metricName, "alert", alertsState.page, pageSize, dateRange?.from, dateRange?.to);

  // Fetch data for recording rules tab
  const {
    data: recordingData,
    isLoading: isRecordingLoading,
    error: recordingError
  } = useMetricUsage(metricName, "record", recordingsState.page, pageSize, dateRange?.from, dateRange?.to);

  // Initialize column definitions
  const queriesColumns = React.useMemo(() => getQueriesColumns(), []);
  const alertsColumns = React.useMemo(() => getAlertsColumns(), []);
  const recordingColumns = React.useMemo(() => getRecordingColumns(), []);
  const dashboardColumns = React.useMemo(() => getDashboardColumns(), []);

  return (
    <Card>
      <CardHeader>
        <CardTitle>Metric Usage</CardTitle>
      </CardHeader>
      <CardContent>
        <Tabs defaultValue="queries" className="w-full">
          <TabsList className="flex bg-gray-100 rounded-lg overflow-hidden w-full">
            <TabsTrigger value="queries" className="flex-1 py-3 px-5">
              Queries
            </TabsTrigger>
            <TabsTrigger value="alerts" className="flex-1 py-3 px-5">
              Alerts
            </TabsTrigger>
            <TabsTrigger value="rules" className="flex-1 py-3 px-5">
              Rules
            </TabsTrigger>
            <TabsTrigger value="dashboards" className="flex-1 py-3 px-5">
              Dashboards
            </TabsTrigger>
          </TabsList>

          <TabsContent value="queries">
            <TabContent
              isLoading={isExpressionsLoading}
              error={expressionsError}
              data={expressionsData}
              columns={queriesColumns}
              state={queriesState}
              searchColumn="query"
              pageSize={pageSize}
              onSortingChange={queriesState.setSorting}
              onFilterChange={queriesState.setFilter}
              onPaginationChange={queriesState.setPage}
            />
          </TabsContent>

          <TabsContent value="alerts">
            <TabContent
              isLoading={isAlertLoading}
              error={alertError}
              data={alertData}
              columns={alertsColumns}
              state={alertsState}
              searchColumn="expression"
              pageSize={pageSize}
              onSortingChange={alertsState.setSorting}
              onFilterChange={alertsState.setFilter}
              onPaginationChange={alertsState.setPage}
            />
          </TabsContent>

          <TabsContent value="rules">
            <TabContent
              isLoading={isRecordingLoading}
              error={recordingError}
              data={recordingData}
              columns={recordingColumns}
              state={recordingsState}
              searchColumn="expression"
              pageSize={pageSize}
              onSortingChange={recordingsState.setSorting}
              onFilterChange={recordingsState.setFilter}
              onPaginationChange={recordingsState.setPage}
            />
          </TabsContent>

          <TabsContent value="dashboards">
            <TabContent
              isLoading={isDashboardLoading}
              error={dashboardError}
              data={dashboardData}
              columns={dashboardColumns}
              state={dashboardsState}
              searchColumn="title"
              pageSize={pageSize}
              onSortingChange={dashboardsState.setSorting}
              onFilterChange={dashboardsState.setFilter}
              onPaginationChange={dashboardsState.setPage}
            />
          </TabsContent>
        </Tabs>
      </CardContent>
    </Card>
  );
} 