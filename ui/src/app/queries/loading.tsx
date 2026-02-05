import { Skeleton } from '@/components/ui/skeleton';

export function LoadingState() {
  return (
    <div className="p-4">
      {/* Search header */}
      <div className="flex items-center justify-between mb-4">
        <div className="flex items-center gap-4">
          <Skeleton className="h-10 w-[300px]" />
        </div>
      </div>

      {/* Table skeleton */}
      <div className="mt-4 rounded-sm border">
        {/* Table header */}
        <div className="border-b">
          <div className="flex h-10 items-center gap-4 px-4">
            <Skeleton className="h-4 w-[400px]" /> {/* Query column */}
            <Skeleton className="h-4 w-[120px]" /> {/* Executions */}
            <Skeleton className="h-4 w-[120px]" /> {/* Avg Duration */}
            <Skeleton className="h-4 w-[120px]" /> {/* Error Rate % */}
            <Skeleton className="h-4 w-[120px]" /> {/* Peak Samples */}
          </div>
        </div>

        {/* Table rows */}
        {Array.from({ length: 10 }).map((_, i) => (
          <div key={i} className="border-b">
            <div className="flex items-center gap-4 px-4 py-3">
              <Skeleton className="h-4 w-[400px]" />
              <Skeleton className="h-4 w-[120px]" />
              <Skeleton className="h-4 w-[120px]" />
              <Skeleton className="h-4 w-[120px]" />
              <Skeleton className="h-4 w-[120px]" />
            </div>
          </div>
        ))}

        {/* Table footer */}
        <div className="flex items-center justify-between border-t px-3 py-2">
          <div className="flex items-center space-x-4 text-sm">
            <Skeleton className="h-4 w-[200px]" />
          </div>
          <div className="flex items-center space-x-2">
            <Skeleton className="h-8 w-8" />
            <Skeleton className="h-4 w-[100px]" />
            <Skeleton className="h-8 w-8" />
          </div>
        </div>
      </div>
    </div>
  );
}
