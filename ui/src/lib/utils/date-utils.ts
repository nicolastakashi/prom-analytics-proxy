import { format, parseISO } from "date-fns";
import { toZonedTime } from "date-fns-tz";

// Convert a local date to UTC for sending to the backend
export function toUTC(date: Date | string): string {
  const dateObj = typeof date === "string" ? new Date(date) : date;
  return dateObj.toISOString();
}

// Convert a UTC date to local timezone for display
export function fromUTC(utcDate: string | Date): Date {
  const dateObj = typeof utcDate === "string" ? parseISO(utcDate) : utcDate;
  return toZonedTime(dateObj, Intl.DateTimeFormat().resolvedOptions().timeZone);
}

// Format a UTC date string to a display format in local timezone
export function formatUTCtoLocal(
  utcDate: string | Date,
  formatString: string = "MMM d, yyyy HH:mm:ss",
): string {
  const localDate = fromUTC(utcDate);
  return format(localDate, formatString);
}

// Format local date to UTC ISO string for API
export function formatLocalToUTC(localDate: Date | string): string {
  const dateObj =
    typeof localDate === "string" ? new Date(localDate) : localDate;
  // For local to UTC, we can simply use toISOString() since it automatically converts to UTC
  return dateObj.toISOString();
}
