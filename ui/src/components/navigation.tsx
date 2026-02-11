import { type LucideIcon } from "lucide-react";
import { Link, useLocation } from "wouter";

import {
  SidebarGroup,
  SidebarGroupLabel,
  SidebarMenu,
  SidebarMenuButton,
  SidebarMenuItem,
} from "@/components/ui/sidebar";

interface NavigationItem {
  name: string;
  url: string;
  icon: LucideIcon;
}

interface NavigationProps {
  label: string;
  items: NavigationItem[];
}

export function Navigation({ label, items }: NavigationProps) {
  const [location] = useLocation();

  return (
    <SidebarGroup className="py-2">
      <SidebarGroupLabel className="px-4 mb-2 text-xs font-medium text-sidebar-foreground/70">
        {label}
      </SidebarGroupLabel>
      <SidebarMenu className="space-y-1.5">
        {items.map((item) => {
          const isActive = location === item.url;

          return (
            <SidebarMenuItem key={item.name}>
              <Link href={item.url}>
                <SidebarMenuButton
                  isActive={isActive}
                  className={`${isActive ? "font-medium" : "font-normal"} transition-all`}
                >
                  <item.icon
                    className={`flex-shrink-0 ${isActive ? "text-primary" : ""}`}
                  />
                  <span className="flex-1">{item.name}</span>
                </SidebarMenuButton>
              </Link>
            </SidebarMenuItem>
          );
        })}
      </SidebarMenu>
    </SidebarGroup>
  );
}
