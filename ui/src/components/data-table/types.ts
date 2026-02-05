import { ColumnDef, SortingState } from '@tanstack/react-table';

// Core DataTable props interface
export interface DataTableProps<TData> {
  data: TData[];
  columns: Array<ColumnDef<TData, unknown> & { maxWidth?: string | number }>;
  searchColumn?: keyof TData;
  pagination?: boolean;
  pageSize?: number;
  pageSizeOptions?: number[];
  onRowClick?: (row: TData) => void;
  className?: string;

  // Server-side operations
  serverSide?: boolean;

  // Controlled state for server-side operations
  sortingState?: SortingState;
  filterValue?: string;
  totalPages?: number;
  currentPage?: number;

  // Callbacks for server-side operations
  onSortingChange?: (sorting: SortingState) => void;
  onFilterChange?: (value: string) => void;
  onPaginationChange?: (page: number) => void;
}

// Sorting props interface
export interface DataTableColumnHeaderProps {
  column: unknown; // Will be typed properly when integrating TanStack Table
  title: string;
  className?: string;
}

// Pagination props interface
export interface DataTablePaginationProps {
  totalPages: number;
  currentPage: number;
  onPageChange: (page: number) => void;
  className?: string;
}

// Filter props interface
export interface DataTableFilterProps {
  placeholder?: string;
  value: string;
  onChange: (value: string) => void;
  className?: string;
}
