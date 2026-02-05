import { useMemo, useState } from 'react';
import { useQuery } from '@tanstack/react-query';
import { Input } from '@/components/ui/input';
import { DataTable, DataTableColumnHeader } from '@/components/data-table';
import type { ColumnDef, SortingState } from '@tanstack/react-table';
import { useDateRange } from '@/contexts/date-range-context';
import { useDebounce } from '@/hooks/use-debounce';
import { getQueryExpressions } from '@/api/queries';
import { LoadingState } from './loading';
import type { PagedResult, QueryExpression } from '@/lib/types';
import {
  Sheet,
  SheetContent,
  SheetDescription,
  SheetHeader,
  SheetTitle,
} from '@/components/ui/sheet';
import { QueryDetails } from '@/components/query-details';
import { useSearchState } from '@/hooks/use-search-state.tsx';

// Extend ColumnDef to support maxWidth so DataTable can apply ellipsis + tooltip
type ExtendedColumnDef<TData, TValue = unknown> = ColumnDef<TData, TValue> & {
  maxWidth?: string | number;
};

const columns: ExtendedColumnDef<QueryExpression>[] = [
  {
    accessorKey: 'query',
    maxWidth: 600,
    header: ({ column }) => (
      <DataTableColumnHeader column={column} title="Query" />
    ),
    cell: ({ row }) => String(row.getValue('query')),
  },
  {
    accessorKey: 'executions',
    header: ({ column }) => (
      <DataTableColumnHeader column={column} title="Executions" />
    ),
    cell: ({ row }) => {
      const value = Number(row.getValue('executions'));
      return <div className="text-right">{value.toLocaleString()}</div>;
    },
  },
  {
    accessorKey: 'avgDuration',
    header: ({ column }) => (
      <DataTableColumnHeader column={column} title="Avg Duration" />
    ),
    cell: ({ row }) => {
      const value = Number(row.getValue('avgDuration'));
      return <div className="text-right">{value}ms</div>;
    },
  },
  {
    accessorKey: 'errorRatePercent',
    header: ({ column }) => (
      <DataTableColumnHeader column={column} title="Error Rate %" />
    ),
    cell: ({ row }) => {
      const value = Number(row.getValue('errorRatePercent'));
      return <div className="text-right">{value}%</div>;
    },
  },
  {
    accessorKey: 'peakSamples',
    header: ({ column }) => (
      <DataTableColumnHeader column={column} title="Peak Samples" />
    ),
    cell: ({ row }) => {
      const value = Number(row.getValue('peakSamples'));
      return <div className="text-right">{value.toLocaleString()}</div>;
    },
  },
];

const SELECTED_QUERY_KEY = 'queriesPageSelectedQuery';
const SEARCH_QUERY_KEY = 'queriesPageSearchQuery';
const PAGE_KEY = 'queriesPagePage';
const PAGE_SIZE_KEY = 'queriesPagePageSize';

export default function QueriesPage() {
  const { dateRange } = useDateRange();
  const fromISO = dateRange?.from?.toISOString();
  const toISO = dateRange?.to?.toISOString();
  const [selectedQuery, setSelectedQuery] = useSearchState<string | null>(
    SELECTED_QUERY_KEY,
    null,
  );
  const [searchQuery, setSearchQuery] = useSearchState<string>(
    SEARCH_QUERY_KEY,
    '',
  );
  const [page, setPage] = useSearchState<number>(PAGE_KEY, 1);
  const [pageSize] = useSearchState<number>(PAGE_SIZE_KEY, 1);
  const debouncedSearch = useDebounce(searchQuery, 750);

  const [sorting, setSorting] = useState<SortingState>([
    { id: 'executions', desc: true },
  ]);

  const handleSortingChange = (newSorting: SortingState) => {
    setSorting(newSorting);
  };

  const handleFilterChange = (value: string) => {
    setSearchQuery(value);
  };

  const handlePaginationChange = (page: number) => {
    setPage(page);
  };

  const { data, isLoading } = useQuery<PagedResult<QueryExpression>>({
    queryKey: [
      'queryExpressions',
      fromISO,
      toISO,
      page,
      pageSize,
      sorting[0]?.id || 'executions',
      sorting[0]?.desc ? 'desc' : 'asc',
      debouncedSearch,
    ],
    queryFn: () =>
      getQueryExpressions(
        fromISO,
        toISO,
        page,
        pageSize,
        sorting[0]?.id || 'executions',
        sorting[0]?.desc ? 'desc' : 'asc',
        debouncedSearch,
      ),
    enabled: Boolean(fromISO && toISO),
  });

  const parsedSelectedQuery = useMemo(() => {
    if (!selectedQuery) return null;
    try {
      return JSON.parse(selectedQuery) as {
        query: string;
        fingerprint?: string;
      };
    } catch {
      return null;
    }
  }, [selectedQuery]);

  if (isLoading) {
    return <LoadingState />;
  }

  return (
    <div className="p-4">
      <div className="mb-4">
        <h1 className="text-2xl font-bold">Queries</h1>
        <p className="text-sm text-muted-foreground">
          Aggregated view of query fingerprints with executions, latency,
          errors, and peak samples.
        </p>
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
          pageSize={pageSize}
          className="w-full"
          serverSide={true}
          sortingState={sorting}
          filterValue={debouncedSearch}
          currentPage={page}
          totalPages={data?.totalPages || 1}
          onSortingChange={handleSortingChange}
          onFilterChange={handleFilterChange}
          onPaginationChange={handlePaginationChange}
          onRowClick={(row) =>
            setSelectedQuery(
              JSON.stringify({
                query: row.query,
                fingerprint: row.fingerprint,
              }),
            )
          }
        />
        <Sheet
          open={!!selectedQuery}
          onOpenChange={() => setSelectedQuery(null)}
        >
          <SheetContent className="w-[1200px] sm:max-w-[1200px]">
            <SheetHeader>
              <SheetTitle>Query Fingerprint Details</SheetTitle>
              <SheetDescription>
                Detailed analysis and performance metrics for the selected query
                pattern
              </SheetDescription>
            </SheetHeader>
            {parsedSelectedQuery && (
              <QueryDetails
                query={parsedSelectedQuery.query}
                fingerprint={parsedSelectedQuery.fingerprint}
                onClose={() => setSelectedQuery(null)}
              />
            )}
          </SheetContent>
        </Sheet>
      </div>
    </div>
  );
}
