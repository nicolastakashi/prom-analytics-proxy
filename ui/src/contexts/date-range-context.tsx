import { createContext, useContext, ReactNode, useState, useEffect } from 'react';
import { useSearchParams } from 'wouter';
import type { DateRange } from 'react-day-picker';
import { fromUTC, formatLocalToUTC } from '@/lib/utils/date-utils';

interface DateRangeContextType {
  dateRange: DateRange | undefined;
  setDateRange: (range: DateRange | undefined) => void;
}

const DateRangeContext = createContext<DateRangeContextType | undefined>(undefined);

export function DateRangeProvider({ children }: { children: ReactNode }) {
  const [searchParams, setSearchParams] = useSearchParams();
  const [dateRange, setInternalDateRange] = useState<DateRange | undefined>(undefined);

  useEffect(() => {
    const from = searchParams.get("from");
    const to = searchParams.get("to");

    if (from && to) {
      try {
        // Convert from UTC to local time for internal use
        const fromDate = fromUTC(from);
        const toDate = fromUTC(to);

        if (!isNaN(fromDate.getTime()) && !isNaN(toDate.getTime())) {
          setInternalDateRange({ from: fromDate, to: toDate });
        }
      } catch (error) {
        console.error("Error parsing dates from URL", error);
      }
    }
  }, [searchParams]);

  const setDateRange = (range: DateRange | undefined) => {
    setInternalDateRange(range);
    if (range?.from && range?.to) {
      // Convert to UTC for URL params
      setSearchParams({
        from: formatLocalToUTC(range.from),
        to: formatLocalToUTC(range.to)
      });
    }
  };

  return (
    <DateRangeContext.Provider value={{ dateRange, setDateRange }}>
      {children}
    </DateRangeContext.Provider>
  );
}

export function useDateRange() {
  const context = useContext(DateRangeContext);
  if (context === undefined) {
    throw new Error('useDateRange must be used within a DateRangeProvider');
  }
  return context;
} 