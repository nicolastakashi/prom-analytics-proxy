"use client"

import { useState, useEffect } from "react"
import { Button } from "@/components/ui/button"
import { Calendar } from "@/components/ui/calendar"
import { Popover, PopoverContent, PopoverTrigger } from "@/components/ui/popover"
import { Select, SelectContent, SelectItem, SelectTrigger, SelectValue } from "@/components/ui/select"
import { Filter, CalendarIcon } from "lucide-react"
import type { DateRange } from "react-day-picker"
import { format, subDays, parse } from "date-fns"
import { usePathname, useRouter, useSearchParams } from "next/navigation"

export function FilterPanel() {
    const router = useRouter()
    const pathname = usePathname()
    const searchParams = useSearchParams()

    // Initialize date state
    const [date, setDate] = useState<DateRange | undefined>(undefined)

    // Load dates from URL or set default on component mount
    useEffect(() => {
        const fromParam = searchParams.get("from")
        const toParam = searchParams.get("to")

        if (fromParam && toParam) {
            try {
                // Try to parse dates from URL parameters (format: YYYY-MM-DD)
                const fromDate = parse(fromParam, "yyyy-MM-dd", new Date())
                const toDate = parse(toParam, "yyyy-MM-dd", new Date())

                // Validate dates are valid
                if (!isNaN(fromDate.getTime()) && !isNaN(toDate.getTime())) {
                    setDate({ from: fromDate, to: toDate })
                    return
                }
            } catch (error) {
                console.error("Error parsing dates from URL", error)
            }
        }

        // Default to last 7 days if no valid URL parameters
        const today = new Date()
        const sevenDaysAgo = subDays(today, 7)
        setDate({ from: sevenDaysAgo, to: today })
    }, [searchParams])

    // Update URL when date changes
    const handleDateChange = (newDate: DateRange | undefined) => {
        setDate(newDate)

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
        router.push(`${pathname}?${params.toString()}`)
    }

    return (
        <div className="flex flex-wrap items-center gap-2">
            <Popover>
                <PopoverTrigger asChild>
                    <Button variant="outline" className="min-w-[240px] justify-start">
                        <CalendarIcon className="mr-2 h-4 w-4" />
                        {date?.from ? (
                            date.to ? (
                                <>
                                    {format(date.from, "LLL dd, y")} - {format(date.to, "LLL dd, y")}
                                </>
                            ) : (
                                format(date.from, "LLL dd, y")
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
                        defaultMonth={date?.from}
                        selected={date}
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

