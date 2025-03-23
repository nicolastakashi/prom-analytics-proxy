import { FilterPanel } from "@/components/filter-panel"
import { KeyMetrics } from "@/components/home/key-metrics"
import Layout from "@/components/layout"
import { getAverageDuration, getQueryRate, getQueryTypes } from "./actions"

interface PageProps {
  searchParams?: Promise<{
    from: string
    to: string
  }>
}

export default async function Page(props: PageProps) {
  const searchParams = await props.searchParams
  const queryTypes = await getQueryTypes(searchParams?.from, searchParams?.to)
  const averageDuration = await getAverageDuration(searchParams?.from, searchParams?.to)
  const queryRate = await getQueryRate(searchParams?.from, searchParams?.to)

  console.log(queryRate)
  return (
    <Layout>
      <div className="mx-auto pl-6 pr-6">
        <div className="mb-6 flex items-center justify-between">
          <h1 className="text-2xl font-bold">Query Analytics</h1>
          <FilterPanel />
        </div>
        <div className="grid gap-6">
          <KeyMetrics 
            queryTypes={queryTypes} 
            averageDuration={averageDuration} 
            queryRate={queryRate} 
          />
          <div className="grid gap-6 lg:grid-cols-2">
            <div className="grid gap-6">
              {/* <StatusBreakdown /> */}
              {/* <QueryPerformanceAnalysis /> */}
            </div>
            <div className="grid gap-6">
              {/* <QueryPerformanceAnalysis /> */}
              {/* <QueryPerformanceAnalysis /> */}
            </div>
          </div>
          {/* <QueryTable /> */}
        </div>
      </div>
      {/* <div className="grid auto-rows-min gap-4 md:grid-cols-3">
        <div className="bg-muted/50 aspect-video rounded-xl" />
        <div className="bg-muted/50 aspect-video rounded-xl" />
        <div className="bg-muted/50 aspect-video rounded-xl" />
      </div>
      <div className="bg-muted/50 min-h-[100vh] flex-1 rounded-xl md:min-h-min" /> */}
    </Layout>
  )
}
