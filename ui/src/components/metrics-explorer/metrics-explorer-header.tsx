import { Input } from "@/components/ui/input"
import { Select, SelectContent, SelectItem, SelectTrigger, SelectValue } from "@/components/ui/select"
import { Filter } from "lucide-react"

interface MetricsExplorerHeaderProps {
  searchQuery: string
  onSearchChange: (value: string) => void
  typeFilter: string
  onTypeFilterChange: (value: string) => void
}

export function MetricsExplorerHeader({ 
  searchQuery, 
  onSearchChange,
  typeFilter,
  onTypeFilterChange
}: MetricsExplorerHeaderProps) {
  return (
    <div className="flex flex-col gap-2">
      <div>
        <h1 className="text-2xl font-bold">Queries</h1>
        <p className="text-sm text-muted-foreground">
          Browse and analyze query patterns in your queries
        </p>
      </div>
      <div className="flex flex-col gap-2 sm:flex-row sm:items-center">
        <Input 
          placeholder="Search queries..." 
          className="sm:max-w-[300px]"
          value={searchQuery}
          onChange={(e) => onSearchChange(e.target.value)}
        />
        <Select value={typeFilter} onValueChange={onTypeFilterChange}>
          <SelectTrigger className="sm:max-w-[140px]">
            <div className="flex items-center gap-2">
              <Filter className="h-4 w-4" />
              <SelectValue placeholder="All Types" />
            </div>
          </SelectTrigger>
          <SelectContent>
            <SelectItem value="all">All Types</SelectItem>
            <SelectItem value="counter">Counter</SelectItem>
            <SelectItem value="gauge">Gauge</SelectItem>
            <SelectItem value="histogram">Histogram</SelectItem>
            <SelectItem value="summary">Summary</SelectItem>
          </SelectContent>
        </Select>
      </div>
    </div>
  )
}
