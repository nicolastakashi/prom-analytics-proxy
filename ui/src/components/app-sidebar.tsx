import * as React from "react"
import {
  Settings,
} from "lucide-react"
import { Link, useLocation } from "wouter"
import logo from "@/assets/logo.png"

import { Navigation } from "@/components/navigation"
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

const Logo: React.FC<{ className?: string }> = ({ className }) => {
  return <img src={logo} alt="Prom Analytics Logo" className={`w-8 h-8 ${className || ''}`} />
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
      <SidebarHeader className="flex h-16 min-h-[4rem] items-center justify-center border-b border-sidebar-border">
        <div className="flex items-center gap-3">
          <Logo />
          {!isCollapsed && <span className="font-semibold">Prom Analytics</span>}
        </div>
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
