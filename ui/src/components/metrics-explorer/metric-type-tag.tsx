import { ArrowUpRight, BarChart3, ChevronUp, Timer, HelpCircle, Hash, Plus } from "lucide-react"
import { Badge } from "@/components/ui/badge"
import { LucideIcon } from "lucide-react"

interface MetricTypeTagProps {
  type: string
}

interface TypeConfig {
  label: string
  icon: LucideIcon
  variant: "outline"
}

type TypeConfigs = {
  [key: string]: TypeConfig
}

const typeConfig: TypeConfigs = {
  counter: {
    label: "Counter",
    icon: ChevronUp,
    variant: "outline",
  },
  gauge: {
    label: "Gauge",
    icon: ArrowUpRight,
    variant: "outline",
  },
  histogram: {
    label: "Histogram",
    icon: BarChart3,
    variant: "outline",
  },
  summary: {
    label: "Summary",
    icon: Timer,
    variant: "outline",
  },
  histogram_bucket: {
    label: "Histogram Bucket",
    icon: BarChart3,
    variant: "outline",
  },
  histogram_count: {
    label: "Histogram Count",
    icon: Hash,
    variant: "outline",
  },
  histogram_sum: {
    label: "Histogram Sum",
    icon: Plus,
    variant: "outline",
  },
  summary_count: {
    label: "Summary Count",
    icon: Hash,
    variant: "outline",
  },
  summary_sum: {
    label: "Summary Sum", 
    icon: Plus,
    variant: "outline",
  },
}

export function MetricTypeTag({ type }: MetricTypeTagProps) {
  // Handle case-insensitive type matching and provide fallback
  const normalizedType = type?.toLowerCase() || 'unknown'
  const config = typeConfig[normalizedType] || {
    label: type || 'Unknown',
    icon: HelpCircle,
    variant: "outline" as const,
  }
  const Icon = config.icon

  return (
    <Badge variant={config.variant} className="gap-1">
      <Icon className="h-3 w-3" />
      {config.label}
    </Badge>
  )
}
