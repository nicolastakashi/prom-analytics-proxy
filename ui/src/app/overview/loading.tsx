import { Skeleton } from "@/components/ui/skeleton";
import { Card, CardContent, CardHeader, CardTitle } from "@/components/ui/card";

export function LoadingState() {
  return (
    <div className="mx-auto pl-6 pr-6">
      <div className="mb-6 flex items-center justify-between">
        <h1 className="text-2xl font-bold">Query Analytics</h1>
        <Skeleton className="h-10 w-[200px]" />
      </div>
      <div className="grid gap-6">
        {/* KeyMetrics loading state */}
        <div className="grid gap-4 md:grid-cols-2 lg:grid-cols-5">
          {/* Query Types Card - spans 2 columns */}
          <Card className="lg:col-span-2 gap-2">
            <CardHeader className="flex flex-row items-center justify-between space-y-0 pb-1 pt-3">
              <CardTitle className="text-sm font-medium">Query Types</CardTitle>
              <Skeleton className="h-4 w-4" />
            </CardHeader>
            <CardContent className="pb-3">
              <div className="flex items-center gap-3">
                <Skeleton className="h-[70px] w-[70px] rounded-full" />
                <div className="space-y-2">
                  <Skeleton className="h-6 w-24" />
                  <Skeleton className="h-3 w-32" />
                  <div className="flex gap-4">
                    <Skeleton className="h-3 w-24" />
                  </div>
                </div>
              </div>
            </CardContent>
          </Card>

          {/* Avg Duration Card */}
          <Card className="gap-2">
            <CardHeader className="flex flex-row items-center justify-between space-y-0 pb-1 pt-3">
              <CardTitle className="text-sm font-medium">
                Avg Duration
              </CardTitle>
              <Skeleton className="h-4 w-4" />
            </CardHeader>
            <CardContent className="pb-3">
              <div className="space-y-2">
                <Skeleton className="h-8 w-24" />
                <Skeleton className="h-3 w-32" />
              </div>
            </CardContent>
          </Card>

          {/* Success Rate Card */}
          <Card className="gap-2">
            <CardHeader className="flex flex-row items-center justify-between space-y-0 pb-1 pt-3">
              <CardTitle className="text-sm font-medium">
                Success Rate
              </CardTitle>
              <Skeleton className="h-4 w-4" />
            </CardHeader>
            <CardContent className="pb-3">
              <div className="space-y-2">
                <Skeleton className="h-8 w-16" />
                <Skeleton className="h-3 w-32" />
              </div>
            </CardContent>
          </Card>

          {/* Error Rate Card */}
          <Card className="gap-2">
            <CardHeader className="flex flex-row items-center justify-between space-y-0 pb-1 pt-3">
              <CardTitle className="text-sm font-medium">Error Rate</CardTitle>
              <Skeleton className="h-4 w-4" />
            </CardHeader>
            <CardContent className="pb-3">
              <div className="space-y-2">
                <Skeleton className="h-8 w-16" />
                <Skeleton className="h-3 w-32" />
              </div>
            </CardContent>
          </Card>
        </div>

        {/* Two column grid layout */}
        <div className="grid gap-6 lg:grid-cols-2">
          <div className="grid gap-6">
            <div className="rounded-lg border p-6">
              <Skeleton className="h-6 w-48 mb-4" />
              <Skeleton className="h-[300px] w-full" />
            </div>
            <div className="rounded-lg border p-6">
              <Skeleton className="h-6 w-48 mb-4" />
              <Skeleton className="h-[300px] w-full" />
            </div>
          </div>
          <div className="grid gap-6">
            <div className="rounded-lg border p-6">
              <Skeleton className="h-6 w-48 mb-4" />
              <Skeleton className="h-[300px] w-full" />
            </div>
            <div className="rounded-lg border p-6">
              <Skeleton className="h-6 w-48 mb-4" />
              <Skeleton className="h-[300px] w-full" />
            </div>
          </div>
        </div>
      </div>
    </div>
  );
}
