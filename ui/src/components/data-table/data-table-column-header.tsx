import { Button } from "@/components/ui/button";
import { ArrowDown, ArrowUp, ArrowUpDown } from "lucide-react";
import { Column } from "@tanstack/react-table";
import { DataTableColumnHeaderProps } from "./types";

export function DataTableColumnHeader({
  column,
  title,
  className,
}: DataTableColumnHeaderProps) {
  // Type assertion for column as it comes from the interface as unknown
  const tableColumn = column as Column<unknown, unknown>;

  if (!tableColumn.getCanSort()) {
    return <div className={className}>{title}</div>;
  }

  return (
    <Button
      variant="ghost"
      className={`px-2 h-8 font-semibold flex items-center justify-between w-full ${className || ""}`}
      onClick={() => tableColumn.toggleSorting()}
    >
      <span>{title}</span>
      {tableColumn.getIsSorted() === "asc" ? (
        <ArrowUp className="ml-2 h-4 w-4" />
      ) : tableColumn.getIsSorted() === "desc" ? (
        <ArrowDown className="ml-2 h-4 w-4" />
      ) : (
        <ArrowUpDown className="ml-2 h-4 w-4 text-gray-400" />
      )}
    </Button>
  );
}
