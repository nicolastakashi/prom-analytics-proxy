import * as React from "react"
import {
  AudioWaveform,
  Command,
  Frame,
  GalleryVerticalEnd,
  Map,
  PieChart,
  Settings,
} from "lucide-react"
import { Link, useLocation } from "wouter"

import { Navigation } from "@/components/navigation"
import { TeamSwitcher } from "@/components/team-switcher"
import {
  Sidebar,
  SidebarContent,
  SidebarFooter,
  SidebarHeader,
  SidebarMenu,
  SidebarMenuButton,
  SidebarMenuItem,
  SidebarRail,
  useSidebar,
} from "@/components/ui/sidebar"

// This is sample data.
const data = {
  teams: [
    {
      name: "Acme Inc",
      logo: GalleryVerticalEnd,
      plan: "Enterprise",
    },
    {
      name: "Acme Corp.",
      logo: AudioWaveform,
      plan: "Startup",
    },
    {
      name: "Evil Corp.",
      logo: Command,
      plan: "Free",
    },
  ],
  analytics: [
    {
      name: "Overview",
      url: "/",
      icon: Frame,
    },
    {
      name: "Performance",
      url: "/performance",
      icon: PieChart,
    },
    {
      name: "Metrics Explorer",
      url: "/metric-explorer",
      icon: Map,
    },
  ],
}

export function AppSidebar({ ...props }: React.ComponentProps<typeof Sidebar>) {
  const [location] = useLocation()
  const { state } = useSidebar()
  const isSettingsActive = location === "/settings"
  const isCollapsed = state === "collapsed"

  return (
    <Sidebar collapsible="icon" {...props}>
      <SidebarHeader className="border-b border-sidebar-border">
        <TeamSwitcher teams={data.teams} />
      </SidebarHeader>
      <SidebarContent className="flex-grow">
        <Navigation 
          label="Analytics" 
          items={data.analytics} 
        />
      </SidebarContent>
      <SidebarFooter className="mt-auto border-t border-sidebar-border py-3">
        {/* Settings Menu Item */}
        <SidebarMenu className={isCollapsed ? "px-0" : "px-2"}>
          <SidebarMenuItem>
            <Link href="/settings">
              <SidebarMenuButton 
                isActive={isSettingsActive}
                className={`
                  hover:bg-sidebar-accent/60 transition-colors 
                  ${isCollapsed ? "justify-center" : ""}
                `}
              >
                <Settings className="flex-shrink-0 text-primary" />
                <span className={`flex-1 font-medium ${isCollapsed ? "hidden" : ""}`}>Settings</span>
              </SidebarMenuButton>
            </Link>
          </SidebarMenuItem>
        </SidebarMenu>
      </SidebarFooter>
      <SidebarRail />
    </Sidebar>
  )
}
