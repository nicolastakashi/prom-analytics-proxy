import { Button } from '@/components/ui/button';
import {
  Popover,
  PopoverContent,
  PopoverTrigger,
} from '@/components/ui/popover';
import { Info, X } from 'lucide-react';
import React from 'react';

interface InfoTooltipProps {
  content: string;
}

export function InfoTooltip({ content }: InfoTooltipProps) {
  const [isOpen, setIsOpen] = React.useState(false);

  return (
    <Popover open={isOpen} onOpenChange={setIsOpen}>
      <PopoverTrigger asChild>
        <Button
          variant="ghost"
          size="sm"
          className="h-11 w-11 rounded-full p-0 hover:bg-muted active:bg-muted/70"
        >
          <Info className="h-5 w-5 text-muted-foreground" />
          <span className="sr-only">More information</span>
        </Button>
      </PopoverTrigger>
      <PopoverContent side="top" align="center" className="w-80">
        <div className="flex items-center justify-between">
          <p className="text-sm">{content}</p>
          <Button
            variant="ghost"
            size="sm"
            className="h-8 w-8 p-0 hover:bg-muted"
            onClick={() => setIsOpen(false)}
          >
            <X className="h-4 w-4" />
            <span className="sr-only">Close</span>
          </Button>
        </div>
      </PopoverContent>
    </Popover>
  );
}
