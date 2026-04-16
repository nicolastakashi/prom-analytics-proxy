import { useState, ReactNode } from "react";
import type { TableState } from "@/lib/types";
import { TableContext } from "@/contexts/table";

export function TableProvider({ children }: { children: ReactNode }) {
  const [tableState, setTableState] = useState<TableState>({
    page: 1,
    type: "",
    pageSize: 10,
    sortBy: "timestamp",
    sortOrder: "desc",
    filter: "",
  });

  return (
    <TableContext.Provider value={{ tableState, setTableState }}>
      {children}
    </TableContext.Provider>
  );
}
