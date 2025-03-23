import { createContext, useContext, ReactNode, useState, useEffect } from 'react';
import { useSearchParams } from 'wouter';
import { parse } from 'date-fns';
import type { DateRange } from 'react-day-picker';

interface DateRangeContextType {
  dateRange: DateRange | undefined;
  setDateRange: (range: DateRange | undefined) => void;
}

const DateRangeContext = createContext<DateRangeContextType | undefined>(undefined);

export function DateRangeProvider({ children }: { children: ReactNode }) {
  const [searchParams] = useSearchParams();
  const [dateRange, setDateRange] = useState<DateRange | undefined>(undefined);

  useEffect(() => {
    const from = searchParams.get("from");
    const to = searchParams.get("to");

    if (from && to) {
      try {
        const fromDate = parse(from, "yyyy-MM-dd", new Date());
        const toDate = parse(to, "yyyy-MM-dd", new Date());

        if (!isNaN(fromDate.getTime()) && !isNaN(toDate.getTime())) {
          setDateRange({ from: fromDate, to: toDate });
        }
      } catch (error) {
        console.error("Error parsing dates from URL", error);
      }
    }
  }, [searchParams]);

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