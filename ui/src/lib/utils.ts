import { clsx, type ClassValue } from 'clsx';
import { twMerge } from 'tailwind-merge';

export function cn(...inputs: ClassValue[]) {
  return twMerge(clsx(inputs));
}

export function formatDuration(ms: number): string {
  // Always display in ms, with up to two decimals for sub-millisecond values
  if (ms < 1) {
    return `${ms.toFixed(2)}ms`;
  }
  if (ms < 10) {
    return `${ms.toFixed(2)}ms`;
  }
  if (ms < 100) {
    return `${ms.toFixed(1)}ms`;
  }
  return `${Math.round(ms)}ms`;
}

export function formatUnit(value: number): string {
  if (value === 0) return '0';

  const absValue = Math.abs(value);

  if (absValue >= 1000000000) {
    return (value / 1000000000).toFixed(2).replace(/\.?0+$/, '') + 'B';
  }

  if (absValue >= 1000000) {
    return (value / 1000000).toFixed(2).replace(/\.?0+$/, '') + 'M';
  }

  if (absValue >= 1000) {
    return (value / 1000).toFixed(2).replace(/\.?0+$/, '') + 'K';
  }

  return value.toString();
}
