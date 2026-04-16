import { createContext, useContext } from "react";
import type { DateRange } from "react-day-picker";

export interface DateRangeContextType {
  dateRange: DateRange;
  setDateRange: (range: DateRange) => void;
}

export const DateRangeContext = createContext<DateRangeContextType | undefined>(
  undefined,
);

export function useDateRange() {
  const context = useContext(DateRangeContext);
  if (context === undefined) {
    throw new Error("useDateRange must be used within a DateRangeProvider");
  }
  return context;
}
