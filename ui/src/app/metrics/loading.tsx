import { Skeleton } from "@/components/ui/skeleton";

export function LoadingState() {
  return (
    <div className="p-4">
      {/* Search and filter header */}
      <div className="flex items-center justify-between mb-4">
        <div className="flex items-center gap-4">
          <Skeleton className="h-10 w-[300px]" /> {/* Search input */}
          <Skeleton className="h-10 w-[120px]" /> {/* Type filter */}
        </div>
      </div>

      {/* Table skeleton */}
      <div className="mt-4 rounded-sm border">
        {/* Table header */}
        <div className="border-b">
          <div className="flex h-10 items-center gap-4 px-4">
            <Skeleton className="h-4 w-[200px]" /> {/* Name column */}
            <Skeleton className="h-4 w-[100px]" /> {/* Type column */}
            <Skeleton className="h-4 w-[300px]" /> {/* Description column */}
          </div>
        </div>

        {/* Table rows */}
        {Array.from({ length: 10 }).map((_, i) => (
          <div key={i} className="border-b">
            <div className="flex items-center gap-4 px-4 py-3">
              <Skeleton className="h-4 w-[200px]" /> {/* Name */}
              <Skeleton className="h-6 w-[100px]" /> {/* Type tag */}
              <Skeleton className="h-4 w-[300px]" /> {/* Description */}
            </div>
          </div>
        ))}

        {/* Table footer */}
        <div className="flex items-center justify-between border-t px-3 py-2">
          <div className="flex items-center space-x-4 text-sm">
            <div className="flex items-center space-x-2">
              <Skeleton className="h-4 w-[100px]" /> {/* Rows per page text */}
              <Skeleton className="h-8 w-[70px]" /> {/* Page size select */}
            </div>
            <Skeleton className="h-4 w-[100px]" /> {/* Total count */}
          </div>
          <div className="flex items-center space-x-2">
            <Skeleton className="h-8 w-8" /> {/* Previous button */}
            <Skeleton className="h-4 w-[100px]" /> {/* Page info */}
            <Skeleton className="h-8 w-8" /> {/* Next button */}
          </div>
        </div>
      </div>
    </div>
  );
}
