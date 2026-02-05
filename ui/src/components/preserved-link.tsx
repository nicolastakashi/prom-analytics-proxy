import { Link, useSearchParams } from 'wouter';
import { ReactNode } from 'react';

export interface LinkProps {
  href: string;
  children: ReactNode;
}

// This component preserves the 'from' and 'to' query parameters when navigating to a new link
export function PreservedLink({ href, children }: LinkProps) {
  const [searchParams] = useSearchParams();

  const from = searchParams.get('from');
  const to = searchParams.get('to');

  let finalHref = href;
  if (from && to) {
    const url = new URL(href, window.location.origin);
    url.searchParams.set('from', from);
    url.searchParams.set('to', to);
    finalHref = url.pathname + url.search;
  }

  return <Link href={finalHref}>{children}</Link>;
}
