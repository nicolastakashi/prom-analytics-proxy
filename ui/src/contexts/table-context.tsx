import { createContext, useContext, useState, ReactNode } from 'react';
import { TableState } from '@/lib/types';

interface TableContextType {
  tableState: TableState;
  setTableState: (state: TableState) => void;
}

const TableContext = createContext<TableContextType | undefined>(undefined);

export function TableProvider({ children }: { children: ReactNode }) {
  const [tableState, setTableState] = useState<TableState>({
    page: 1,
    type: '',
    pageSize: 10,
    sortBy: 'timestamp',
    sortOrder: 'desc',
    filter: '',
  });

  return (
    <TableContext.Provider value={{ tableState, setTableState }}>
      {children}
    </TableContext.Provider>
  );
}

export function useTable() {
  const context = useContext(TableContext);
  if (context === undefined) {
    throw new Error('useTable must be used within a TableProvider');
  }
  return context;
}
