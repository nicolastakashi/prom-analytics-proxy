// import { MetricLabels } from "@/components/metrics-explorer/metric-labels"
// import MetricUsage from "@/components/metrics-explorer/metric-usage"
// import { MetricPerformance } from "@/components/metrics-explorer/metric-performance"
// import { MetricRecommendations } from "@/components/metrics-explorer/metric-recommendations"
import { Tabs, TabsContent, TabsList, TabsTrigger } from "@/components/ui/tabs"
import { Card, CardContent } from "@/components/ui/card"
import { Bell, BarChart3, GitMerge, Info, X, Database } from "lucide-react"
import { Button } from "@/components/ui/button"
import { Popover, PopoverContent, PopoverTrigger } from "@/components/ui/popover"
import React from "react"
import { useParams } from "wouter"
import { MetricDetailHeader } from "@/components/metrics-explorer/metric-detail-header"
import { LoadingState } from "@/app/overview/loading"
import { useMetricStatistics } from "../use-metrics-data"
import { useDateRange } from "@/contexts/date-range-context"
import { MetricPerformance } from "@/components/metrics-explorer/metric-performance"

interface InfoTooltipProps {
    content: string
  }
  
  function InfoTooltip({ content }: InfoTooltipProps) {
    const [isOpen, setIsOpen] = React.useState(false)
  
    return (
      <Popover open={isOpen} onOpenChange={setIsOpen}>
        <PopoverTrigger asChild>
          <Button variant="ghost" size="sm" className="h-11 w-11 rounded-full p-0 hover:bg-muted active:bg-muted/70">
            <Info className="h-5 w-5 text-muted-foreground" />
            <span className="sr-only">More information</span>
          </Button>
        </PopoverTrigger>
        <PopoverContent side="top" align="center" className="w-80">
          <div className="flex items-center justify-between">
            <p className="text-sm">{content}</p>
            <Button variant="ghost" size="sm" className="h-8 w-8 p-0 hover:bg-muted" onClick={() => setIsOpen(false)}>
              <X className="h-4 w-4" />
              <span className="sr-only">Close</span>
            </Button>
          </div>
        </PopoverContent>
      </Popover>
    )
  }
  

export default function MetricsDetails() {
    const { metric } = useParams();
    const { dateRange } = useDateRange();
    const { data, isLoading, error } = useMetricStatistics(metric || "", dateRange);

    if (isLoading) {
        return <LoadingState />
    }

    if (error) {
        return <div>Error: {error.message}</div>
    }
    
    return (
        <div className="p-6">
          <MetricDetailHeader metricName={metric || ""} />
          <div className="grid gap-4 md:grid-cols-4 mt-6">
            <Card>
              <CardContent>
                <div className="flex flex-col gap-1">
                  <div className="flex items-center justify-between">
                    <div className="flex items-center gap-2">
                      <span className="text-sm text-muted-foreground">Series Count</span>
                      <InfoTooltip content="Number of unique time series currently tracked for this metric across all label combinations. High cardinality can impact storage and query performance." />
                    </div>
                    <Database className="h-4 w-4 text-muted-foreground" />
                  </div>
                  <div className="flex items-baseline gap-2">
                    <span className="text-2xl font-bold">{data.statistics?.serie_count}</span>
                  </div>
                  <span className="text-xs text-muted-foreground">across {data.statistics?.label_count} labels</span>
                </div>
              </CardContent>
            </Card>
            <Card>
              <CardContent>
                <div className="flex flex-col gap-1">
                  <div className="flex items-center justify-between">
                    <div className="flex items-center gap-2">
                      <span className="text-sm text-muted-foreground">Alerts</span>
                      <InfoTooltip content="Total number of alert rules using this metric, including both firing and pending alerts." />
                    </div>
                    <Bell className="h-4 w-4 text-muted-foreground" />
                  </div>
                  <div className="flex items-baseline gap-2">
                    <span className="text-2xl font-bold">{data.statistics?.alert_count}</span>
                  </div>
                  <span className="text-xs text-muted-foreground">across {data.statistics?.total_alerts} alerts</span>
                </div>
              </CardContent>
            </Card>
            <Card>
              <CardContent>
                <div className="flex flex-col gap-1">
                  <div className="flex items-center justify-between">
                    <div className="flex items-center gap-2">
                      <span className="text-sm text-muted-foreground">Recording Rules</span>
                      <InfoTooltip content="Pre-computed expressions that aggregate or transform this metric into new time series, helping improve query performance and reduce load on the database." />
                    </div>
                    <GitMerge className="h-4 w-4 text-muted-foreground" />
                  </div>
                  <div className="flex items-baseline gap-2">
                    <span className="text-2xl font-bold">{data.statistics?.record_count}</span>
                  </div>
                  <span className="text-xs text-muted-foreground">across {data.statistics?.total_records} records</span>
                </div>
              </CardContent>
            </Card>
            <Card>
              <CardContent>
                <div className="flex flex-col gap-1">
                  <div className="flex items-center justify-between">
                    <div className="flex items-center gap-2">
                      <span className="text-sm text-muted-foreground">Dashboards</span>
                      <InfoTooltip content="Number of dashboards currently using this metric in their visualizations and queries. This helps track the metric's usage across your monitoring dashboards." />
                    </div>
                    <BarChart3 className="h-4 w-4 text-muted-foreground" />
                  </div>
                  <div className="flex items-baseline gap-2">
                    <span className="text-2xl font-bold">{data.statistics?.dashboard_count}</span>
                  </div>
                  <span className="text-xs text-muted-foreground">across {data.statistics?.total_dashboards} dashboards</span>
                </div>
              </CardContent>
            </Card>
          </div>
          <div className="mt-6">
          <div className="mt-6">
        <Tabs defaultValue="performance">
          <TabsList className="flex bg-gray-100 rounded-lg overflow-hidden">
            <TabsTrigger value="performance" className="flex-1 py-3 px-5 text-center cursor-pointer transition-colors duration-300 hover:bg-white data-[state=active]:bg-white font-semibold">
              Performance
            </TabsTrigger>
            <TabsTrigger value="labels" className="flex-1 py-3 px-5 text-center cursor-pointer transition-colors duration-300 hover:bg-white data-[state=active]:bg-white font-semibold">
              Labels & Dimensions
            </TabsTrigger>
            <TabsTrigger value="usage" className="flex-1 py-3 px-5 text-center cursor-pointer transition-colors duration-300 hover:bg-white data-[state=active]:bg-white font-semibold">
              Usage
            </TabsTrigger>
            <TabsTrigger value="recommendations" className="flex-1 py-3 px-5 text-center cursor-pointer transition-colors duration-300 hover:bg-white data-[state=active]:bg-white font-semibold">
              Recommendations
            </TabsTrigger>
          </TabsList>
          <TabsContent value="performance" className=" bg-white rounded-lg mt-2">
              <MetricPerformance />
            </TabsContent>
          <TabsContent value="labels" className="p-4 bg-white rounded-lg mt-2">
            <div className="space-y-6">
              {/* <MetricLabels metricName={params.name} /> */}
            </div>
          </TabsContent>
          <TabsContent value="usage" className="p-4 bg-white rounded-lg mt-2">
            {/* <MetricUsage metricName={params.name} /> */}
          </TabsContent>
          <TabsContent value="recommendations" className="p-4 bg-white rounded-lg mt-2">
            {/* <MetricRecommendations metricName={params.name} /> */}
          </TabsContent>
        </Tabs>
      </div>
          </div>
        </div>
      )
}
