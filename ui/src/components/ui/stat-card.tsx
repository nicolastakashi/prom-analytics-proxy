import { Card, CardContent } from "@/components/ui/card"
import { InfoTooltip } from "@/components/ui/info-tooltip"
import { LucideIcon } from "lucide-react"
import { cn } from "@/lib/utils"

interface StatCardProps {
  title: string
  value: string | number
  icon: LucideIcon
  tooltipContent: string
  showStatusIndicator?: boolean
  statusColor?: string
}

export function StatCard({
  title,
  value,
  icon: Icon,
  tooltipContent,
  showStatusIndicator = false,
  statusColor,
}: StatCardProps) {
  return (
    <Card className="relative overflow-hidden">
      <CardContent>
        <div className="space-y-2">
          <div className="flex items-center justify-between">
            <div className="flex items-center gap-2">
              <p className="text-sm text-muted-foreground">{title}</p>
              <InfoTooltip content={tooltipContent} />
            </div>
            <Icon className="h-4 w-4 text-muted-foreground" />
          </div>
          {showStatusIndicator ? (
            <div className="flex items-center gap-2">
              <div className={cn("h-2 w-2 rounded-full", statusColor)} />
              <p className="text-2xl font-bold">{value}</p>
            </div>
          ) : (
            <p className="text-2xl font-bold">{value}</p>
          )}
        </div>
      </CardContent>
    </Card>
  )
} 