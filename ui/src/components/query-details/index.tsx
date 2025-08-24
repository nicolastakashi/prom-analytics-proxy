import { Copy } from "lucide-react"
import { Button } from "../ui/button"
import { toast } from "sonner"
import { Tabs, TabsContent, TabsList, TabsTrigger } from "../ui/tabs"
import { KeyMetrics } from "../key-metrics"
import QueryTimeRangeDistribution from "../query-time-range-distribution"
import { QueryLatencyTrends } from "../query-latency-trends"

interface QueryDetailsProps {
  onClose: () => void
  query: string
  fingerprint?: string
}

export function QueryDetails({ query, fingerprint }: QueryDetailsProps) {
  const copyToClipboard = () => {
    navigator.clipboard.writeText(query)
    toast.success("Query copied to clipboard")
  }
  
  return (
    <div className="overflow-y-auto max-h-[calc(90vh-120px)] pr-4 pl-4">
      <div className="mb-6 p-3 bg-foreground/2 rounded-lg border flex items-center justify-between gap-3">
        <code className="text-sm font-mono flex-1 break-all">{query}</code>
        <Button variant="ghost" size="sm" onClick={copyToClipboard} className="shrink-0 cursor-pointer">
          <Copy className="h-4 w-4" />
        </Button>
      </div>
      <Tabs defaultValue="overview">
          <TabsList className="flex bg-gray-100 rounded-lg overflow-hidden w-full grid-cols-5">
            <TabsTrigger value="overview" className="flex-1 py-3 px-5">Overview</TabsTrigger>
          </TabsList>
          <TabsContent value="overview" className="mt-2 grid gap-4">
            <KeyMetrics fingerprint={fingerprint} />
            <QueryTimeRangeDistribution fingerprint={fingerprint} />
            <QueryLatencyTrends fingerprint={fingerprint} />
          </TabsContent>
        </Tabs>
    </div>
  )
}