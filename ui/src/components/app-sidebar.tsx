import * as React from "react";
import { useQuery } from "@tanstack/react-query";
import { Settings } from "lucide-react";
import { useLocation } from "wouter";
import logo from "@/assets/logo.png";

import { getFeatures } from "@/api/metrics";
import { Navigation } from "@/components/navigation";
import {
  Sidebar,
  SidebarContent,
  SidebarFooter,
  SidebarHeader,
  SidebarMenu,
  SidebarMenuButton,
  SidebarMenuItem,
  SidebarRail,
} from "@/components/ui/sidebar";
import { useSidebar } from "@/components/ui/sidebar-context";
import { routeConfigs, ROUTES } from "@/lib/routes";
import { PreservedLink } from "@/components/preserved-link.tsx";

const Logo: React.FC<{ className?: string }> = ({ className }) => {
  return (
    <img
      src={logo}
      alt="Prom Analytics Logo"
      className={`w-8 h-8 ${className || ""}`}
    />
  );
};

export function AppSidebar({ ...props }: React.ComponentProps<typeof Sidebar>) {
  const [location] = useLocation();
  const { state } = useSidebar();
  const { data: features } = useQuery({
    queryKey: ["features"],
    queryFn: getFeatures,
    staleTime: Infinity,
  });
  const isSettingsActive = location === "/settings";
  const isCollapsed = state === "collapsed";
  const producerStatsEnabled = features?.producer_stats_enabled ?? false;
  const navigationItems = routeConfigs
    .filter(
      (route) =>
        route.navigation?.showInSidebar &&
        (route.path !== ROUTES.PRODUCERS || producerStatsEnabled),
    )
    .map((route) => ({
      name: route.navigation!.name,
      url: route.path,
      icon: route.navigation!.icon,
    }));

  return (
    <Sidebar collapsible="icon" {...props}>
      <SidebarHeader className="flex h-16 min-h-[4rem] items-center justify-center border-b border-sidebar-border">
        <div className="flex items-center gap-3">
          <Logo />
          {!isCollapsed && (
            <span className="font-semibold">Prom Analytics</span>
          )}
        </div>
      </SidebarHeader>
      <SidebarContent className="flex-grow">
        <Navigation label="Analytics" items={navigationItems} />
      </SidebarContent>
      <SidebarFooter className="mt-auto border-t border-sidebar-border py-3">
        {/* Settings Menu Item */}
        <SidebarMenu className={isCollapsed ? "px-0" : "px-2"}>
          <SidebarMenuItem>
            <PreservedLink href="/settings">
              <SidebarMenuButton
                isActive={isSettingsActive}
                className={`
                  hover:bg-sidebar-accent/60 transition-colors 
                  ${isCollapsed ? "justify-center" : ""}
                `}
              >
                <Settings className="flex-shrink-0 text-primary" />
                <span
                  className={`flex-1 font-medium ${isCollapsed ? "hidden" : ""}`}
                >
                  Settings
                </span>
              </SidebarMenuButton>
            </PreservedLink>
          </SidebarMenuItem>
        </SidebarMenu>
      </SidebarFooter>
      <SidebarRail />
    </Sidebar>
  );
}
