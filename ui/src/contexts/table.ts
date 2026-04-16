import { createContext, useContext } from "react";
import type { TableState } from "@/lib/types";

interface TableContextType {
  tableState: TableState;
  setTableState: (state: TableState) => void;
}

export const TableContext = createContext<TableContextType | undefined>(
  undefined,
);

export function useTable() {
  const context = useContext(TableContext);
  if (context === undefined) {
    throw new Error("useTable must be used within a TableProvider");
  }
  return context;
}
