import { useMemo } from "react";
import { useQuery } from "@tanstack/react-query";
import { DataTable, DataTableColumnHeader } from "@/components/data-table";
import type { ColumnDef, SortingState } from "@tanstack/react-table";
import { useDateRange } from "@/contexts/date-range-context";
import { getQueryExecutions } from "@/api/queries";
import type { PagedResult, QueryExecution } from "@/lib/types";
import { formatUTCtoLocal } from "@/lib/utils/date-utils";
import { Badge } from "@/components/ui/badge";
import {
  Tooltip,
  TooltipContent,
  TooltipTrigger,
} from "@/components/ui/tooltip";
import { useSearchParams } from "wouter";

type ExtendedColumnDef<TData, TValue = unknown> = ColumnDef<TData, TValue> & {
  maxWidth?: string | number;
};

// Generate consistent colors for chip values
const generateColorForValue = (value: string): string => {
  let hash = 0;
  for (let i = 0; i < value.length; i++) {
    const char = value.charCodeAt(i);
    hash = (hash << 5) - hash + char;
    hash = hash & hash; // Convert to 32bit integer
  }

  // Generate HSL color with higher saturation and lower lightness for more vibrant colors
  const hue = Math.abs(hash) % 360;
  return `hsl(${hue}, 75%, 75%)`;
};

// Format time range duration in human-readable form
const formatTimeRangeDuration = (start: string, end: string): string => {
  try {
    const startTime = new Date(start).getTime();
    const endTime = new Date(end).getTime();
    const diffMs = endTime - startTime;

    if (diffMs < 0) return "-";

    const seconds = Math.floor(diffMs / 1000);
    const minutes = Math.floor(seconds / 60);
    const hours = Math.floor(minutes / 60);
    const days = Math.floor(hours / 24);

    if (days > 0) {
      const remainingHours = hours % 24;
      return remainingHours > 0 ? `${days}d ${remainingHours}h` : `${days}d`;
    }
    if (hours > 0) {
      const remainingMinutes = minutes % 60;
      return remainingMinutes > 0
        ? `${hours}h ${remainingMinutes}m`
        : `${hours}h`;
    }
    if (minutes > 0) {
      const remainingSeconds = seconds % 60;
      return remainingSeconds > 0
        ? `${minutes}m ${remainingSeconds}s`
        : `${minutes}m`;
    }
    if (seconds > 0) {
      return `${seconds}s`;
    }
    return `${diffMs}ms`;
  } catch {
    return "-";
  }
};

const HTTPHeadersChips: React.FC<{ httpHeaders: Record<string, string> }> = ({
  httpHeaders,
}) => {
  // Filter out entries with empty keys or values
  const entries = Object.entries(httpHeaders).filter(
    ([key, value]) => key.trim() !== "" && value.trim() !== "",
  );
  const maxVisibleChips = 3;

  if (entries.length === 0) {
    return <span className="text-muted-foreground">-</span>;
  }

  const visibleChips = entries.slice(0, maxVisibleChips);
  const hiddenChips = entries.slice(maxVisibleChips);

  const ChipContent: React.FC<{ chipKey: string; value: string }> = ({
    chipKey,
    value,
  }) => {
    const chipText = `${chipKey}: ${value}`;
    const maxLength = 30;

    if (chipText.length <= maxLength) {
      return (
        <Badge
          variant="outline"
          className="text-xs border"
          style={{
            backgroundColor: generateColorForValue(value),
            borderColor: generateColorForValue(value),
            color: "#374151",
          }}
        >
          {chipText}
        </Badge>
      );
    }

    return (
      <Tooltip>
        <TooltipTrigger asChild>
          <Badge
            variant="outline"
            className="text-xs border cursor-help"
            style={{
              backgroundColor: generateColorForValue(value),
              borderColor: generateColorForValue(value),
              color: "#374151",
            }}
          >
            {chipText.substring(0, maxLength - 3)}...
          </Badge>
        </TooltipTrigger>
        <TooltipContent
          side="top"
          className="bg-slate-900 text-white border-slate-700 px-3 py-2"
        >
          <span className="font-mono text-xs">{chipText}</span>
        </TooltipContent>
      </Tooltip>
    );
  };

  return (
    <div className="flex flex-wrap gap-1 items-center">
      {visibleChips.map(([key, value]) => (
        <ChipContent key={key} chipKey={key} value={value} />
      ))}
      {hiddenChips.length > 0 && (
        <Tooltip>
          <TooltipTrigger asChild>
            <Badge variant="outline" className="text-xs cursor-help">
              +{hiddenChips.length} more
            </Badge>
          </TooltipTrigger>
          <TooltipContent
            side="top"
            className="max-w-xs bg-slate-900 text-white border-slate-700 px-3 py-2"
          >
            <div className="flex flex-wrap gap-1">
              {hiddenChips.map(([key, value]) => (
                <ChipContent key={key} chipKey={key} value={value} />
              ))}
            </div>
          </TooltipContent>
        </Tooltip>
      )}
    </div>
  );
};

const columns: ExtendedColumnDef<QueryExecution>[] = [
  {
    accessorKey: "timestamp",
    header: ({ column }) => (
      <DataTableColumnHeader column={column} title="Timestamp" />
    ),
    cell: ({ row }) => {
      const value = String(row.getValue("timestamp"));
      try {
        return (
          <div className="flex items-baseline gap-2">
            <span className="font-medium">
              {formatUTCtoLocal(value, "dd/MM/yyyy")}
            </span>
            <span className="text-xs text-muted-foreground">
              {formatUTCtoLocal(value, "HH:mm")}
            </span>
          </div>
        );
      } catch {
        return "-";
      }
    },
  },
  {
    accessorKey: "type",
    header: ({ column }) => (
      <DataTableColumnHeader column={column} title="Type" />
    ),
    cell: ({ row }) => {
      const t = String(row.getValue("type"));
      const label = t === "range" ? "Range" : "Instant";
      return <Badge variant="outline">{label}</Badge>;
    },
  },
  {
    accessorKey: "status",
    header: ({ column }) => (
      <DataTableColumnHeader column={column} title="Status" />
    ),
    cell: ({ row }) => {
      const code = Number(row.getValue("status"));
      const isSuccess = code >= 200 && code < 300;
      const isTimeout = code === 0 || code === 408 || code === 504;
      const classes = isSuccess
        ? "bg-emerald-100 text-emerald-700 border-emerald-200"
        : isTimeout
          ? "bg-amber-100 text-amber-700 border-amber-200"
          : "bg-red-100 text-red-700 border-red-200";
      return (
        <Badge variant="outline" className={classes}>
          <span className="font-mono">{code}</span>
        </Badge>
      );
    },
  },
  {
    accessorKey: "duration",
    header: ({ column }) => (
      <DataTableColumnHeader column={column} title="Duration" />
    ),
    cell: ({ row }) => `${Number(row.getValue("duration"))}ms`,
  },
  {
    accessorKey: "samples",
    header: ({ column }) => (
      <DataTableColumnHeader column={column} title="Samples" />
    ),
    cell: ({ row }) => {
      const v = Number(row.getValue("samples"));
      return (
        <div className="text-right">
          {Number.isFinite(v) ? v.toLocaleString() : "-"}
        </div>
      );
    },
  },
  {
    accessorKey: "timeRange",
    header: ({ column }) => (
      <DataTableColumnHeader column={column} title="Time Range" />
    ),
    cell: ({ row }) => {
      const type = String(row.original.type);
      const start = row.original.start;
      const end = row.original.end;

      const renderTooltip = (
        content: React.ReactNode,
        tooltipContent: React.ReactNode,
      ) => (
        <Tooltip>
          <TooltipTrigger asChild>{content}</TooltipTrigger>
          <TooltipContent
            side="top"
            className="bg-slate-900 text-white border-slate-700 px-3 py-2"
          >
            {tooltipContent}
          </TooltipContent>
        </Tooltip>
      );

      // Show "-" for instant queries with tooltip showing the start time
      if (type === "instant") {
        if (start) {
          try {
            const formattedStart = formatUTCtoLocal(
              start,
              "dd/MM/yyyy HH:mm:ss",
            );
            return renderTooltip(
              <span className="text-muted-foreground cursor-help">-</span>,
              <span className="font-mono text-xs">{formattedStart}</span>,
            );
          } catch {
            return <span className="text-muted-foreground">-</span>;
          }
        }
        return <span className="text-muted-foreground">-</span>;
      }

      // Show duration for range queries if we have both start and end
      if (start && end) {
        try {
          const formattedStart = formatUTCtoLocal(start, "dd/MM/yyyy HH:mm:ss");
          const formattedEnd = formatUTCtoLocal(end, "dd/MM/yyyy HH:mm:ss");
          const duration = formatTimeRangeDuration(start, end);

          return renderTooltip(
            <span className="font-medium cursor-help">{duration}</span>,
            <div className="font-mono text-xs">
              <div>{formattedStart}</div>
              <div className="text-slate-400">to</div>
              <div>{formattedEnd}</div>
            </div>,
          );
        } catch {
          return (
            <span className="font-medium">
              {formatTimeRangeDuration(start, end)}
            </span>
          );
        }
      }

      return <span className="text-muted-foreground">-</span>;
    },
  },
  {
    accessorKey: "steps",
    header: ({ column }) => (
      <DataTableColumnHeader column={column} title="Steps" />
    ),
    cell: ({ row }) => String(row.getValue("steps")),
  },
  {
    accessorKey: "httpHeaders",
    header: ({ column }) => (
      <DataTableColumnHeader column={column} title="HTTP Headers" />
    ),
    cell: ({ row }) => {
      const httpHeaders = row.getValue("httpHeaders") as
        | Record<string, string>
        | undefined;
      if (!httpHeaders || Object.keys(httpHeaders).length === 0) {
        return <span className="text-muted-foreground">-</span>;
      }
      return <HTTPHeadersChips httpHeaders={httpHeaders} />;
    },
  },
];

const PAGE_KEY = "queryExecutionsPage";
const PAGE_SIZE_KEY = "queryExecutionsPageSize";
const SORTING_KEY = "queryExecutionsSorting";

interface Props {
  fingerprint?: string;
}

export const QueryExecutions: React.FC<Props> = ({ fingerprint }) => {
  const { dateRange } = useDateRange();
  const fromISO = dateRange?.from?.toISOString();
  const toISO = dateRange?.to?.toISOString();

  const [searchParams, setSearchParams] = useSearchParams();
  const page: number = Number(searchParams.get(PAGE_KEY)) || 1;
  const pageSize: number = Number(searchParams.get(PAGE_SIZE_KEY)) || 10;
  const sorting: SortingState = useMemo(
    () =>
      searchParams.get(SORTING_KEY)
        ? (JSON.parse(searchParams.get(SORTING_KEY) as string) as SortingState)
        : [
            {
              id: "timestamp",
              desc: true,
            },
          ],
    [searchParams],
  );

  const handleSortingChange = (newSorting: SortingState) => {
    setSearchParams((prev) => {
      prev.set(SORTING_KEY, JSON.stringify(newSorting));
      return prev;
    });
  };

  const handlePageChange = (newPage: number) => {
    setSearchParams((prev) => {
      prev.set(PAGE_KEY, newPage.toString());
      return prev;
    });
  };

  const sortBy = useMemo(() => sorting[0]?.id || "timestamp", [sorting]);
  const serverSortBy = useMemo(() => {
    switch (sortBy) {
      case "timestamp":
        return "ts";
      case "status":
        return "statusCode";
      default:
        return sortBy;
    }
  }, [sortBy]);
  const sortOrder = useMemo(
    () => (sorting[0]?.desc ? "desc" : "asc"),
    [sorting],
  );

  const { data, isLoading } = useQuery<PagedResult<QueryExecution>>({
    queryKey: [
      "queryExecutions",
      fingerprint,
      fromISO,
      toISO,
      page,
      pageSize,
      serverSortBy,
      sortOrder,
    ],
    queryFn: () =>
      getQueryExecutions(
        fingerprint || "",
        fromISO,
        toISO,
        page,
        pageSize,
        serverSortBy,
        sortOrder,
        "all",
      ),
    enabled: Boolean(fingerprint),
  });

  if (isLoading) {
    return <div className="p-2 text-sm text-muted-foreground">Loading...</div>;
  }

  return (
    <DataTable<QueryExecution>
      data={data?.data || []}
      columns={columns}
      pagination={true}
      pageSize={pageSize}
      className="w-full"
      serverSide={true}
      sortingState={sorting}
      currentPage={page}
      totalPages={data?.totalPages || 1}
      onSortingChange={(s) => handleSortingChange(s)}
      onPaginationChange={(p) => handlePageChange(p)}
    />
  );
};
