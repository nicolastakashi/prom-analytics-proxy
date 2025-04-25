import { clsx, type ClassValue } from "clsx"
import { twMerge } from "tailwind-merge"

export function cn(...inputs: ClassValue[]) {
  return twMerge(clsx(inputs))
}

export function formatDuration(ms: number): string {
    if (ms < 1) {
        return `${Math.round(ms * 1000)}µs`
    }
    if (ms < 1000) {
        return `${Math.round(ms)}ms`
    }
    if (ms < 60000) {
        return `${Math.round(ms / 1000)}s`
    }
    if (ms < 3600000) {
        return `${Math.round(ms / 60000)}m`
    }
    return `${Math.round(ms / 3600000)}h`
}

export function formatUnit(value: number): string {
    if (value === 0) return '0'
    
    const absValue = Math.abs(value)
    
    if (absValue >= 1000000000) {
        return (value / 1000000000).toFixed(2).replace(/\.?0+$/, '') + 'B'
    }
    
    if (absValue >= 1000000) {
        return (value / 1000000).toFixed(2).replace(/\.?0+$/, '') + 'M'
    }
    
    if (absValue >= 1000) {
        return (value / 1000).toFixed(2).replace(/\.?0+$/, '') + 'K'
    }
    
    return value.toString()
}
