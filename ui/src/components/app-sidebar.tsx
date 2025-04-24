import * as React from "react"
import {
  AudioWaveform,
  Command,
  GalleryVerticalEnd,
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
import { routeConfigs } from "@/lib/routes"

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
}

// Get navigation items from routes configuration
const navigationItems = routeConfigs
  .filter(route => route.navigation?.showInSidebar)
  .map(route => ({
    name: route.navigation!.name,
    url: route.path,
    icon: route.navigation!.icon,
  }))

export function AppSidebar({ ...props }: React.ComponentProps<typeof Sidebar>) {
  const [location] = useLocation()
  const { state } = useSidebar()
  const isSettingsActive = location === "/settings"
  const isCollapsed = state === "collapsed"

  return (
    <Sidebar collapsible="icon" {...props}>
      <SidebarHeader className={`flex h-16 min-h-[4rem] items-center justify-${isCollapsed ? "center" : "start"} border-b border-sidebar-border`}>
        <TeamSwitcher teams={data.teams} />
      </SidebarHeader>
      <SidebarContent className="flex-grow">
        <Navigation 
          label="Analytics" 
          items={navigationItems}
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
