import { Input } from "@/components/ui/input";
import { Search } from "lucide-react";
import { DataTableFilterProps } from "./types";

export function DataTableFilter({
  placeholder = "Search...",
  value,
  onChange,
  className,
}: DataTableFilterProps) {
  return (
    <div className={`relative mb-4 ${className || ""}`}>
      <Search className="absolute left-2 top-1/2 h-4 w-4 -translate-y-1/2 text-gray-500" />
      <Input
        placeholder={placeholder}
        value={value}
        onChange={(e) => onChange(e.target.value)}
        className="max-w-sm pl-8"
      />
    </div>
  );
}
