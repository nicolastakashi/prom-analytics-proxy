import { useState } from "react";
import { useLocation } from "wouter";
import { useQuery } from "@tanstack/react-query";
import type { ColumnDef, SortingState } from "@tanstack/react-table";
import { Input } from "@/components/ui/input";
import { Progress } from "@/components/ui/progress";
import { DataTable, DataTableColumnHeader } from "@/components/data-table";
import { useDebounce } from "@/hooks/use-debounce";
import { useSearchNumberState, useSearchState } from "@/hooks/use-search-state";
import { getProducerStats } from "@/api/metrics";
import { ROUTES } from "@/lib/routes";
import type { PagedResult, ProducerStats } from "@/lib/types";
import { LoadingState } from "./loading";

const SEARCH_KEY = "producersPageSearch";
const PAGE_KEY = "producersPagePage";

export default function ProducersPage() {
  const [, setLocation] = useLocation();
  const [searchQuery, setSearchQuery] = useSearchState(SEARCH_KEY, "");
  const [page, setPage] = useSearchNumberState(PAGE_KEY, 1);
  const [sorting, setSorting] = useState<SortingState>([
    { id: "metricCount", desc: true },
  ]);
  const debouncedSearch = useDebounce(searchQuery, 750);
  const pageSize = 10;

  const { data, isLoading, error } = useQuery<PagedResult<ProducerStats>>({
    queryKey: [
      "producerStats",
      page,
      pageSize,
      sorting[0]?.id,
      sorting[0]?.desc,
      debouncedSearch,
    ],
    queryFn: () =>
      getProducerStats(
        page,
        pageSize,
        sorting[0]?.id || "metricCount",
        sorting[0]?.desc ? "desc" : "asc",
        debouncedSearch,
      ),
  });

  const openProducer = (job: string) => {
    const params = new URLSearchParams({ job });
    setLocation(`${ROUTES.METRICS_EXPLORER}?${params.toString()}`);
  };

  const columns: ColumnDef<ProducerStats>[] = [
    {
      accessorKey: "job",
      size: 320,
      header: ({ column }) => (
        <DataTableColumnHeader column={column} title="Producer" />
      ),
      cell: ({ row }) => (
        <span className="font-medium" title={row.original.job}>
          {row.original.job}
        </span>
      ),
    },
    {
      accessorKey: "contribution",
      size: 240,
      header: ({ column }) => (
        <DataTableColumnHeader column={column} title="Contribution" />
      ),
      cell: ({ row }) => {
        const value = row.original.contribution;
        return (
          <div className="flex min-w-[180px] items-center gap-3">
            <Progress value={value} className="h-2 flex-1" />
            <span className="w-14 text-right tabular-nums">
              {value.toFixed(1)}%
            </span>
          </div>
        );
      },
    },
    {
      accessorKey: "metricCount",
      header: ({ column }) => (
        <DataTableColumnHeader column={column} title="Metrics" />
      ),
      cell: ({ row }) => (
        <div className="text-right tabular-nums">
          {row.original.metricCount.toLocaleString()}
        </div>
      ),
    },
    {
      accessorKey: "usedMetricCount",
      header: ({ column }) => (
        <DataTableColumnHeader column={column} title="Used" />
      ),
      cell: ({ row }) => (
        <div className="text-right tabular-nums">
          {row.original.usedMetricCount.toLocaleString()}
        </div>
      ),
    },
    {
      accessorKey: "unusedMetricCount",
      header: ({ column }) => (
        <DataTableColumnHeader column={column} title="Unused" />
      ),
      cell: ({ row }) => (
        <div className="text-right tabular-nums">
          {row.original.unusedMetricCount.toLocaleString()}
        </div>
      ),
    },
  ];

  if (isLoading) {
    return <LoadingState />;
  }

  if (error) {
    return <div className="p-4">Error: {error.message}</div>;
  }

  return (
    <div className="p-4">
      <div className="mb-4">
        <h1 className="text-2xl font-bold">Producers</h1>
        <p className="text-sm text-muted-foreground">
          Compare the metrics contributed by each producer and identify unused
          instrumentation.
        </p>
      </div>
      <Input
        placeholder="Search producers or metrics..."
        className="mb-4 sm:max-w-[300px]"
        value={searchQuery}
        onChange={(event) => {
          setSearchQuery(event.target.value);
          setPage(1);
        }}
      />
      <DataTable<ProducerStats>
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
        onSortingChange={(nextSorting) => {
          setSorting(nextSorting);
          setPage(1);
        }}
        onFilterChange={setSearchQuery}
        onPaginationChange={setPage}
        onRowClick={(row) => openProducer(row.job)}
      />
    </div>
  );
}
