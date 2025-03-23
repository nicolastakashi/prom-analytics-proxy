interface OverviewLayoutProps {
  children: React.ReactNode;
}

export function OverviewLayout({ children }: OverviewLayoutProps) {
  return (
    <div className="mx-auto pl-6 pr-6">
      {children}
    </div>
  );
} 