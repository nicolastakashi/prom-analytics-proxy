import { Skeleton } from "@/components/ui/skeleton";

export function LoadingState() {
  return (
    <div className="p-4">
      <div className="mb-4 space-y-2">
        <Skeleton className="h-8 w-40" />
        <Skeleton className="h-4 w-[420px] max-w-full" />
      </div>
      <Skeleton className="mb-4 h-10 w-[300px] max-w-full" />
      <div className="rounded-md border">
        {Array.from({ length: 11 }).map((_, index) => (
          <div
            key={index}
            className="flex h-12 items-center gap-6 border-b px-4 last:border-b-0"
          >
            <Skeleton className="h-4 flex-1" />
            <Skeleton className="h-4 w-24" />
            <Skeleton className="h-4 w-20" />
            <Skeleton className="h-4 w-20" />
            <Skeleton className="h-4 w-20" />
          </div>
        ))}
      </div>
    </div>
  );
}
