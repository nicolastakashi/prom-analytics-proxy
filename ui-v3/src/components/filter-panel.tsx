"use client"

import { useEffect } from "react"
import { Button } from "@/components/ui/button"
import { Calendar } from "@/components/ui/calendar"
import { Popover, PopoverContent, PopoverTrigger } from "@/components/ui/popover"
import { Select, SelectContent, SelectItem, SelectTrigger, SelectValue } from "@/components/ui/select"
import { Filter, CalendarIcon } from "lucide-react"
import type { DateRange } from "react-day-picker"
import { format, subDays } from "date-fns"
import { useLocation, useSearchParams } from "wouter"
import { useDateRange } from "@/contexts/date-range-context"

export function FilterPanel() {
    const [pathname, setPathname] = useLocation()
    const [searchParams, setSearchParams] = useSearchParams()
    const { dateRange, setDateRange } = useDateRange()

    // Set default date range if none exists
    useEffect(() => {
        const fromParam = searchParams.get("from")
        const toParam = searchParams.get("to")

        if (!fromParam || !toParam) {
            const today = new Date()
            const sevenDaysAgo = subDays(today, 7)
            
            const params = new URLSearchParams(searchParams.toString())
            params.set("from", format(sevenDaysAgo, "yyyy-MM-dd"))
            params.set("to", format(today, "yyyy-MM-dd"))
            setSearchParams(params.toString())
        }
    }, [searchParams, setSearchParams])

    // Update URL when date changes
    const handleDateChange = (newDate: DateRange | undefined) => {
        setDateRange(newDate)

        // Create new URLSearchParams object based on current params
        const params = new URLSearchParams(searchParams.toString())

        if (newDate?.from) {
            // Format date as YYYY-MM-DD for URL
            params.set("from", format(newDate.from, "yyyy-MM-dd"))

            if (newDate.to) {
                params.set("to", format(newDate.to, "yyyy-MM-dd"))
            } else {
                params.delete("to")
            }
        } else {
            // If date is cleared, remove parameters
            params.delete("from")
            params.delete("to")
        }

        // Update URL with new parameters
        setPathname(`${pathname}?${params.toString()}`)
    }

    return (
        <div className="flex flex-wrap items-center gap-2">
            <Popover>
                <PopoverTrigger asChild>
                    <Button variant="outline" className="min-w-[240px] justify-start">
                        <CalendarIcon className="mr-2 h-4 w-4" />
                        {dateRange?.from ? (
                            dateRange.to ? (
                                <>
                                    {format(dateRange.from, "LLL dd, y")} - {format(dateRange.to, "LLL dd, y")}
                                </>
                            ) : (
                                format(dateRange.from, "LLL dd, y")
                            )
                        ) : (
                            "Select date range"
                        )}
                    </Button>
                </PopoverTrigger>
                <PopoverContent className="w-auto p-0" align="start">
                    <Calendar
                        initialFocus
                        mode="range"
                        defaultMonth={dateRange?.from}
                        selected={dateRange}
                        onSelect={handleDateChange}
                        numberOfMonths={1}
                    />
                </PopoverContent>
            </Popover>
            <Select defaultValue="all">
                <SelectTrigger className="w-[180px]">
                    <Filter className="mr-2 h-4 w-4" />
                    <SelectValue placeholder="Status" />
                </SelectTrigger>
                <SelectContent>
                    <SelectItem value="all">All Status</SelectItem>
                    <SelectItem value="200">Success (200)</SelectItem>
                    <SelectItem value="400">Client Error (400)</SelectItem>
                    <SelectItem value="500">Server Error (500)</SelectItem>
                </SelectContent>
            </Select>
        </div>
    )
}

