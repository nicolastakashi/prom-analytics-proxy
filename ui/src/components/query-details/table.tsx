import { useMemo, useState } from "react"
import { useQuery } from "@tanstack/react-query"
import { DataTable, DataTableColumnHeader } from "@/components/data-table"
import type { ColumnDef, SortingState } from "@tanstack/react-table"
import { useDateRange } from "@/contexts/date-range-context"
import { getQueryExecutions } from "@/api/queries"
import type { PagedResult, QueryExecution } from "@/lib/types"
import { formatUTCtoLocal } from "@/lib/utils/date-utils"
import { Badge } from "@/components/ui/badge"

type ExtendedColumnDef<TData, TValue = unknown> = ColumnDef<TData, TValue> & { maxWidth?: string | number }

const columns: ExtendedColumnDef<QueryExecution>[] = [
  {
    accessorKey: "timestamp",
    header: ({ column }) => (
      <DataTableColumnHeader column={column} title="Timestamp" />
    ),
    cell: ({ row }) => {
      const value = String(row.getValue("timestamp"))
      try {
        return (
          <div className="flex items-baseline gap-2">
            <span className="font-medium">{formatUTCtoLocal(value, 'dd/MM/yyyy')}</span>
            <span className="text-xs text-muted-foreground">{formatUTCtoLocal(value, 'HH:mm')}</span>
          </div>
        )
      } catch {
        return '-'
      }
    },
  },
  {
    accessorKey: "type",
    header: ({ column }) => (
      <DataTableColumnHeader column={column} title="Type" />
    ),
    cell: ({ row }) => {
      const t = String(row.getValue("type"))
      const label = t === 'range' ? 'Range' : 'Instant'
      const variant: "default" | "secondary" | "destructive" | "outline" =
        t === 'range' ? 'default' : 'outline'
      return <Badge variant={variant}>{label}</Badge>
    },
  },
  {
    accessorKey: "status",
    header: ({ column }) => (
      <DataTableColumnHeader column={column} title="Status" />
    ),
    cell: ({ row }) => {
      const code = Number(row.getValue("status"))
      const isSuccess = code >= 200 && code < 300
      const isTimeout = code === 0 || code === 408 || code === 504
      const classes = isSuccess
        ? "bg-emerald-100 text-emerald-700 border-emerald-200"
        : isTimeout
          ? "bg-amber-100 text-amber-700 border-amber-200"
          : "bg-red-100 text-red-700 border-red-200"
      return (
        <Badge variant="outline" className={classes}>
          <span className="font-mono">{code}</span>
        </Badge>
      )
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
      const v = Number(row.getValue("samples"))
      return <div className="text-right">{Number.isFinite(v) ? v.toLocaleString() : "-"}</div>
    },
  },
  {
    accessorKey: "steps",
    header: ({ column }) => (
      <DataTableColumnHeader column={column} title="Steps" />
    ),
    cell: ({ row }) => String(row.getValue("steps")),
  },
]

interface Props { fingerprint?: string }

export const QueryExecutions: React.FC<Props> = ({ fingerprint }) => {
  const { dateRange } = useDateRange()
  const fromISO = dateRange?.from?.toISOString()
  const toISO = dateRange?.to?.toISOString()

  const [page, setPage] = useState(1)
  const pageSize = 10
  const [sorting, setSorting] = useState<SortingState>([
    { id: "timestamp", desc: true },
  ])

  const sortBy = useMemo(() => sorting[0]?.id || "timestamp", [sorting])
  const serverSortBy = useMemo(() => {
    switch (sortBy) {
      case 'timestamp':
        return 'ts'
      case 'status':
        return 'statusCode'
      default:
        return sortBy
    }
  }, [sortBy])
  const sortOrder = useMemo(() => (sorting[0]?.desc ? "desc" : "asc"), [sorting])

  const { data, isLoading } = useQuery<PagedResult<QueryExecution>>({
    queryKey: ["queryExecutions", fingerprint, fromISO, toISO, page, pageSize, serverSortBy, sortOrder],
    queryFn: () => getQueryExecutions(fingerprint || "", fromISO, toISO, page, pageSize, serverSortBy, sortOrder, "all"),
    enabled: Boolean(fingerprint),
  })

  if (isLoading) {
    return <div className="p-2 text-sm text-muted-foreground">Loading...</div>
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
      onSortingChange={(s) => setSorting(s)}
      onPaginationChange={(p) => setPage(p)}
    />
  )
}


