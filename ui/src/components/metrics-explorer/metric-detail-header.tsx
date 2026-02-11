"use client";

import { Button } from "@/components/ui/button";
import { ArrowLeft } from "lucide-react";
import { Link } from "wouter";

interface MetricDetailHeaderProps {
  metricName: string;
}

export function MetricDetailHeader({ metricName }: MetricDetailHeaderProps) {
  return (
    <div className="space-y-6">
      <div className="flex items-center gap-4">
        <Button variant="ghost" size="icon" asChild>
          <Link href="/metrics-explorer">
            <ArrowLeft className="h-4 w-4" />
            <span className="sr-only">Back</span>
          </Link>
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
