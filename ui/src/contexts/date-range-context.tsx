import { ReactNode, useMemo, useCallback } from "react";
import { useSearchParams } from "wouter";
import type { DateRange } from "react-day-picker";
import { DateRangeContext } from "@/contexts/date-range";
import { fromUTC, formatLocalToUTC } from "@/lib/utils/date-utils";

export function DateRangeProvider({ children }: { children: ReactNode }) {
  const [searchParams, setSearchParams] = useSearchParams();

  const dateRange: DateRange = useMemo(() => {
    const from = searchParams.get("from");
    const to = searchParams.get("to");

    if (from && to) {
      try {
        // Convert from UTC to local time for internal use
        const fromDate = fromUTC(from);
        const toDate = fromUTC(to);

        if (!isNaN(fromDate.getTime()) && !isNaN(toDate.getTime())) {
          return { from: fromDate, to: toDate };
        }
      } catch (error) {
        console.error("Error parsing dates from URL", error);
      }
    }

    // Default to last 15 minutes
    const now = new Date();
    // Zero milliseconds for consistency
    now.setMilliseconds(0);
    const fifteenMinutesAgo = new Date(now.getTime() - 15 * 60 * 1000);
    return {
      from: fifteenMinutesAgo,
      to: now,
    };
  }, [searchParams]);

  const setDateRange = useCallback(
    (range: DateRange) => {
      if (range.from && range.to) {
        setSearchParams((prev) => {
          prev.set("from", formatLocalToUTC(range.from!));
          prev.set("to", formatLocalToUTC(range.to!));
          return prev;
        });
      }
    },
    [setSearchParams],
  );

  return (
    <DateRangeContext.Provider value={{ dateRange, setDateRange }}>
      {children}
    </DateRangeContext.Provider>
  );
}
