import {
  createContext,
  useContext,
  ReactNode,
  useMemo,
  useCallback,
} from "react";
import { useSearchParams } from "wouter";
import type { DateRange } from "react-day-picker";
import { fromUTC, formatLocalToUTC } from "@/lib/utils/date-utils";

interface DateRangeContextType {
  dateRange: DateRange | undefined;
  setDateRange: (range: DateRange) => void;
}

const DateRangeContext = createContext<DateRangeContextType | undefined>(
  undefined,
);

export function DateRangeProvider({ children }: { children: ReactNode }) {
  const [searchParams, setSearchParams] = useSearchParams();

  const dateRange: DateRange | undefined = useMemo(() => {
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
    return undefined;
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

export function useDateRange() {
  const context = useContext(DateRangeContext);
  if (context === undefined) {
    throw new Error("useDateRange must be used within a DateRangeProvider");
  }
  return context;
}
