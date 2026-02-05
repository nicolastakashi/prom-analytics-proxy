'use client';

import { Button } from '@/components/ui/button';
import { ArrowLeft } from 'lucide-react';
import { PreservedLink } from '@/components/preserved-link.tsx';

interface MetricDetailHeaderProps {
  metricName: string;
}

export function MetricDetailHeader({ metricName }: MetricDetailHeaderProps) {
  return (
    <div className="space-y-6">
      <div className="flex items-center gap-4">
        <Button variant="ghost" size="icon" asChild>
          <PreservedLink href="/metrics-explorer">
            <ArrowLeft className="h-4 w-4" />
            <span className="sr-only">Back</span>
          </PreservedLink>
        </Button>
        <div>
          <h1 className="text-2xl font-bold">{metricName}</h1>
          <p className="text-sm text-muted-foreground">
            Detailed insights and analysis
          </p>
        </div>
      </div>
    </div>
  );
}
