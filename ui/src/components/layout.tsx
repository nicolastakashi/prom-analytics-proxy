import { AppSidebar } from '@/components/app-sidebar';
import { useLocation } from 'wouter';
import {
  Breadcrumb,
  BreadcrumbItem,
  BreadcrumbLink,
  BreadcrumbList,
  BreadcrumbPage,
  BreadcrumbSeparator,
} from '@/components/ui/breadcrumb';
import { Separator } from '@/components/ui/separator';
import {
  SidebarInset,
  SidebarProvider,
  SidebarTrigger,
} from '@/components/ui/sidebar';
import { ErrorBoundaryWithToast } from './error-boundary';
import { FilterPanel } from '@/components/filter-panel';

interface BreadcrumbData {
  parent?: {
    label: string;
    href: string;
  };
  current: string;
}

interface LayoutProps {
  children: React.ReactNode;
  breadcrumb?: BreadcrumbData;
}

export default function Layout({ children, breadcrumb }: LayoutProps) {
  const [location] = useLocation();
  const showFilterPanel =
    location !== '/metrics-explorer' && location !== '/settings';

  return (
    <ErrorBoundaryWithToast>
      <SidebarProvider>
        <div className="flex w-full">
          <AppSidebar />
          <SidebarInset className="flex flex-col w-full">
            <header className="sticky top-0 z-20 flex h-16 shrink-0 items-center border-b border-sidebar-border bg-background/95 backdrop-blur supports-[backdrop-filter]:bg-background/60">
              <div className="flex w-full items-center justify-between px-4">
                <div className="flex items-center gap-2">
                  <SidebarTrigger className="-ml-1" />
                  <Separator
                    orientation="vertical"
                    className="mr-2 data-[orientation=vertical]:h-4"
                  />
                  <Breadcrumb>
                    <BreadcrumbList>
                      {breadcrumb?.parent && (
                        <>
                          <BreadcrumbItem className="hidden md:block">
                            <BreadcrumbLink href={breadcrumb.parent.href}>
                              {breadcrumb.parent.label}
                            </BreadcrumbLink>
                          </BreadcrumbItem>
                          <BreadcrumbSeparator className="hidden md:block" />
                        </>
                      )}
                      <BreadcrumbItem>
                        <BreadcrumbPage>
                          {breadcrumb?.current || 'Overview'}
                        </BreadcrumbPage>
                      </BreadcrumbItem>
                    </BreadcrumbList>
                  </Breadcrumb>
                </div>
                <div className="flex items-center gap-4">
                  {showFilterPanel && <FilterPanel />}
                </div>
              </div>
            </header>
            <div className="flex-1 w-full">{children}</div>
          </SidebarInset>
        </div>
      </SidebarProvider>
    </ErrorBoundaryWithToast>
  );
}
