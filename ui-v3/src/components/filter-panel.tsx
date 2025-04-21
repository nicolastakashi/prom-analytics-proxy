"use client"

import { useEffect, useState, useMemo, useCallback } from "react"
import { Button } from "@/components/ui/button"
import { Calendar } from "@/components/ui/calendar"
import { Popover, PopoverContent, PopoverTrigger } from "@/components/ui/popover"
import { CalendarIcon, RefreshCw } from "lucide-react"
import type { DateRange, DayClickEventHandler } from "react-day-picker"
import { format, subDays, subHours, startOfDay, endOfDay, differenceInMilliseconds } from "date-fns"
import { useSearchParams } from "wouter"
import { useDateRange } from "@/contexts/date-range-context"
import { fromUTC } from "@/lib/utils/date-utils"

// Types
interface TimeRange {
    from: Date
    to: Date
    label: string
}

function generateQuickRanges(): TimeRange[] {
    const now = new Date()
    const startOfToday = startOfDay(now)
    
    return [
        {
            label: "Last 15 minutes",
            from: subHours(now, 0.25),
            to: now,
        },
        {
            label: "Last hour",
            from: subHours(now, 1),
            to: now,
        },
        {
            label: "Last 6 hours",
            from: subHours(now, 6),
            to: now,
        },
        {
            label: "Last 24 hours",
            from: subHours(now, 24),
            to: now,
        },
        {
            label: "Last 7 days",
            from: startOfDay(subDays(startOfToday, 7)),
            to: now,
        },
        {
            label: "Last 30 days",
            from: startOfDay(subDays(startOfToday, 30)),
            to: now,
        },
        {
            label: "Last 90 days",
            from: startOfDay(subDays(startOfToday, 90)),
            to: now,
        },
    ]
}

export function FilterPanel() {
    const { dateRange, setDateRange } = useDateRange()
    const [searchParams] = useSearchParams()
    const [isOpen, setIsOpen] = useState(false)
    const [isRefreshing, setIsRefreshing] = useState(false)
    
    // Local state for the calendar selection
    const [calendarState, setCalendarState] = useState<DateRange | undefined>(dateRange)
    
    // Time states
    const [fromTime, setFromTime] = useState("00:00")
    const [toTime, setToTime] = useState("23:59")

    // Memoize quick ranges
    const quickRanges = useMemo(generateQuickRanges, [])

    // Initialize from URL params
    useEffect(() => {
        const fromParam = searchParams.get("from")
        const toParam = searchParams.get("to")
        
        if (fromParam && toParam) {
            const from = fromUTC(fromParam)
            const to = fromUTC(toParam)
            setCalendarState({ from, to })
            setDateRange({ from, to })
            setFromTime(format(from, "HH:mm"))
            setToTime(format(to, "HH:mm"))
        } else {
            // Default to last 7 days
            const now = new Date()
            const sevenDaysAgo = subDays(now, 7)
            const range = { 
                from: startOfDay(sevenDaysAgo), 
                to: endOfDay(now) 
            }
            setCalendarState(range)
            setDateRange(range)
        }
    }, [])

    const handleDayClick: DayClickEventHandler = (day, modifiers) => {
        if (modifiers.disabled) return

        const range = {
            from: calendarState?.from,
            to: calendarState?.to
        }

        if (!range.from) {
            range.from = day
        } else if (range.to) {
            range.from = day
            range.to = undefined
        } else if (day < range.from) {
            range.to = range.from
            range.from = day
        } else {
            range.to = day
            // Automatically apply when range is complete
            const newFrom = new Date(range.from)
            const newTo = new Date(day)
            
            // Apply times
            const [fromHours, fromMinutes] = fromTime.split(":")
            const [toHours, toMinutes] = toTime.split(":")
            
            newFrom.setHours(parseInt(fromHours), parseInt(fromMinutes), 0, 0)
            newTo.setHours(parseInt(toHours), parseInt(toMinutes), 59, 999)

            // Update state - dateRange context will handle UTC conversion
            setDateRange({ from: newFrom, to: newTo })
            setIsOpen(false)
            return
        }

        setCalendarState(range)
    }

    const handleQuickRange = useCallback((range: TimeRange) => {
        setFromTime(format(range.from, "HH:mm"))
        setToTime(format(range.to, "HH:mm"))
        
        // DateRange context handles UTC conversion
        setDateRange({ from: range.from, to: range.to })
        setCalendarState({ from: range.from, to: range.to })
        setIsOpen(false)
    }, [setDateRange])

    // Handle refresh functionality
    const handleRefresh = useCallback(() => {
        if (!dateRange?.from || !dateRange?.to) return
        
        setIsRefreshing(true)
        
        try {
            const now = new Date()
            const oldFrom = new Date(dateRange.from)
            const oldTo = new Date(dateRange.to)
            
            // Calculate the time difference to maintain the range duration
            const rangeDuration = differenceInMilliseconds(oldTo, oldFrom)
            
            // Create new range with same duration but updated to current time
            const newFrom = new Date(now.getTime() - rangeDuration)
            const newTo = now
            
            // Update the date range
            setDateRange({ from: newFrom, to: newTo })
            setCalendarState({ from: newFrom, to: newTo })
            
            // Update time inputs
            setFromTime(format(newFrom, "HH:mm"))
            setToTime(format(newTo, "HH:mm"))
        } catch (error) {
            console.error("Error refreshing date range", error)
        } finally {
            // Add small delay to show the refresh animation
            setTimeout(() => {
                setIsRefreshing(false)
            }, 500)
        }
    }, [dateRange, setDateRange])

    // Add keyboard shortcut for refresh (Ctrl+R)
    useEffect(() => {
        const handleKeyDown = (event: KeyboardEvent) => {
            if ((event.ctrlKey || event.metaKey) && event.key === 'r') {
                event.preventDefault() // Prevent browser refresh
                handleRefresh()
            }
        }
        
        window.addEventListener('keydown', handleKeyDown)
        return () => window.removeEventListener('keydown', handleKeyDown)
    }, [handleRefresh])

    return (
        <div className="flex flex-wrap items-center gap-2">
            <Popover open={isOpen} onOpenChange={setIsOpen}>
                <PopoverTrigger asChild>
                    <Button variant="outline" className="min-w-[300px] justify-start">
                        <CalendarIcon className="mr-2 h-4 w-4" />
                        {dateRange?.from && dateRange?.to ? (
                            <>
                                {format(dateRange.from, "MMM d, yyyy HH:mm")} - 
                                {format(dateRange.to, "MMM d, yyyy HH:mm")}
                            </>
                        ) : (
                            "Select date range"
                        )}
                    </Button>
                </PopoverTrigger>
                <PopoverContent className="w-[720px] p-0" align="start">
                    <div className="flex">
                        {/* Quick ranges sidebar */}
                        <div className="w-[200px] border-r p-4 space-y-1">
                            <h4 className="text-sm font-medium text-muted-foreground mb-3">Quick ranges</h4>
                            {quickRanges.map((range) => (
                                <Button
                                    key={range.label}
                                    variant="ghost"
                                    className="w-full justify-start text-left h-8 px-2 text-sm"
                                    onClick={() => handleQuickRange(range)}
                                >
                                    {range.label}
                                </Button>
                            ))}
                        </div>

                        <div className="flex-1 p-4">
                            {/* Time inputs */}
                            <div className="flex gap-4 mb-4">
                                <TimeInput
                                    label="From"
                                    value={fromTime}
                                    onChange={setFromTime}
                                />
                                <TimeInput
                                    label="To"
                                    value={toTime}
                                    onChange={setToTime}
                                />
                            </div>

                            {/* Calendar */}
                            <Calendar
                                mode="range"
                                defaultMonth={calendarState?.from}
                                selected={calendarState}
                                onDayClick={handleDayClick}
                                numberOfMonths={2}
                                modifiers={{
                                    selected: (date) => {
                                        if (!calendarState?.from || !calendarState?.to) return false;
                                        return date >= calendarState.from && date <= calendarState.to;
                                    },
                                    start: (date) => calendarState?.from ? date.getTime() === calendarState.from.getTime() : false,
                                    end: (date) => calendarState?.to ? date.getTime() === calendarState.to.getTime() : false
                                }}
                            />
                        </div>
                    </div>

                    {/* Footer */}
                    <div className="border-t p-3 flex justify-end gap-2">
                        <Button
                            variant="outline"
                            onClick={() => {
                                setCalendarState(dateRange)
                                setIsOpen(false)
                            }}
                        >
                            Cancel
                        </Button>
                        <Button
                            onClick={() => {
                                setCalendarState(dateRange)
                                setIsOpen(false)
                            }}
                        >
                            Apply
                        </Button>
                    </div>
                </PopoverContent>
            </Popover>
            
            {/* Refresh button */}
            <Button
                variant="outline"
                size="icon"
                onClick={handleRefresh}
                disabled={isRefreshing || !dateRange?.from || !dateRange?.to}
                aria-label="Refresh time range"
                title="Refresh time range (Ctrl+R)"
                className="h-10 w-10"
            >
                <RefreshCw className={`h-4 w-4 ${isRefreshing ? 'animate-spin' : ''}`} />
            </Button>
        </div>
    )
}

// Helper components
function TimeInput({ label, value, onChange }: {
    label: string
    value: string
    onChange: (value: string) => void
}) {
    return (
        <div className="grid gap-2 flex-1">
            <label className="text-sm font-medium">{label}</label>
            <input
                type="time"
                value={value}
                onChange={(e) => onChange(e.target.value)}
                className="rounded-md border border-input px-3 py-2 text-sm"
            />
        </div>
    )
}
