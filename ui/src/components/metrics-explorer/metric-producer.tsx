"use client"

import * as React from "react"
import {
  type ColumnDef,
  type SortingState,
} from "@tanstack/react-table"
import { ArrowUpDown } from "lucide-react"
import { Progress } from "@/components/ui/progress"
import { Producer } from "@/lib/types"
import { DataTable } from "@/components/data-table"
import { Card, CardHeader, CardTitle, CardContent } from "@/components/ui/card"

interface ProducersTableProps {
  producers: Producer[]
}

export function MetricProducers({ producers }: ProducersTableProps) {
  // Sorting state (like metric-usage)
  const [sorting, setSorting] = React.useState<SortingState>([])

  // Calculate total series for percentage calculations
  const totalSeries = producers.reduce((sum, producer) => sum + producer.series, 0)

  // Memoize columns (like metric-usage)
  const columns = React.useMemo<ColumnDef<Producer>[]>(() => [
    {
      accessorKey: "job",
      header: ({ column }) => {
        return (
          <div
            className="flex cursor-pointer items-center"
            onClick={() => column.toggleSorting(column.getIsSorted() === "asc")}
          >
            Producer
            <ArrowUpDown className="ml-2 h-4 w-4" />
          </div>
        )
      },
      cell: ({ row }) => <div className="font-medium">{row.getValue("job")}</div>,
    },
    {
      id: "contribution",
      header: () => <div className="text-right">Contribution</div>,
      cell: ({ row }) => {
        const series = row.getValue("series") as number
        const percentage = Math.round((series / totalSeries) * 100)
        return (
          <div className="flex items-center gap-2 justify-end">
            <Progress value={percentage} className="h-1.5" />
            <span className="text-xs text-muted-foreground">{percentage}%</span>
          </div>
        )
      },
    },
    {
      accessorKey: "series",
      header: ({ column }) => {
        return (
          <div
            className="flex cursor-pointer items-center justify-end"
            onClick={() => column.toggleSorting(column.getIsSorted() === "asc")}
          >
            Series
            <ArrowUpDown className="ml-2 h-4 w-4" />
          </div>
        )
      },
      cell: ({ row }) => {
        const series = row.getValue("series") as number
        return (
          <div className="text-right">{series.toLocaleString()}</div>
        )
      },
    },
  ], [totalSeries])

  return (
    <Card>
      <CardHeader>
        <CardTitle>Metric Producers</CardTitle>
      </CardHeader>
      <CardContent>
        <DataTable
          data={producers}
          columns={columns}
          searchColumn="job"
          pagination={true}
          pageSize={10}
          sortingState={sorting}
          onSortingChange={setSorting}
        />
      </CardContent>
    </Card>
  )
}
