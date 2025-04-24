import { lazy } from 'react'
import { Frame, Map } from "lucide-react"
import type { LucideIcon } from "lucide-react"
import type { RouteComponentProps } from "wouter"
import { Overview } from '@/app/overview'

export interface RouteConfig {
  path: string
  component: React.ComponentType<RouteComponentProps>
  breadcrumb: {
    current: string
  }
  navigation?: {
    name: string
    icon: LucideIcon
    showInSidebar: boolean
  }
}

const MetricsExplorer = lazy(() => import('@/app/metrics'))
const MetricsDetails = lazy(() => import('@/app/metrics/details'))

export const ROUTES = {
  HOME: '/',
  OVERVIEW: '/',
  METRICS_EXPLORER: '/metrics-explorer',
  METRIC_EXPLORER: '/metrics-explorer', // For backward compatibility
  METRICS_DETAILS: '/metrics-explorer/:metric',
  METRIC_DETAILS: '/metrics-explorer/:metric', // For backward compatibility
} as const

export type RoutePath = (typeof ROUTES)[keyof typeof ROUTES]

export const routeConfigs: readonly RouteConfig[] = [
  {
    path: ROUTES.OVERVIEW,
    component: Overview,
    breadcrumb: {
      current: "Overview"
    },
    navigation: {
      name: "Overview",
      icon: Frame,
      showInSidebar: true
    }
  },
  {
    path: ROUTES.METRICS_EXPLORER,
    component: MetricsExplorer,
    breadcrumb: {
      current: "Metrics Explorer"
    },
    navigation: {
      name: "Metrics Explorer",
      icon: Map,
      showInSidebar: true
    }
  },
  {
    path: ROUTES.METRICS_DETAILS,
    component: MetricsDetails,
    breadcrumb: {
      current: "Metric Details"
    },
    navigation: {
      name: "Metrics Explorer",
      icon: Map,
      showInSidebar: false
    }
  }
] as const 