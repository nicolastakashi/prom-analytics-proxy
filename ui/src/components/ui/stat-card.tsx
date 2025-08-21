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
    <Card className="relative overflow-hidden py-1.5 md:h-[112px] lg:h-[116px]">
      <CardContent className="pb-1.5 pt-1">
        <div className="space-y-1.5">
          <div className="flex items-center justify-between">
            <div className="flex items-center gap-1.5">
              <p className="text-sm text-muted-foreground">{title}</p>
              <InfoTooltip content={tooltipContent} />
            </div>
            <Icon className="h-4 w-4 text-muted-foreground" />
          </div>
          {showStatusIndicator ? (
            <div className="flex items-center gap-2">
              <div className={cn("h-2 w-2 rounded-full", statusColor)} />
              <p className="text-xl font-bold leading-none">{value}</p>
            </div>
          ) : (
            <p className="text-xl font-bold leading-none">{value}</p>
          )}
        </div>
      </CardContent>
    </Card>
  )
} 