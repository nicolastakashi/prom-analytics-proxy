import { useState } from "react";
import { SortingState } from "@tanstack/react-table";

export interface UseTableStateOptions {
  defaultPage?: number;
  defaultPageSize?: number;
  defaultSortBy?: string;
  defaultSortOrder?: "asc" | "desc";
  defaultFilter?: string;
}

export interface UseTableStateResult {
  page: number;
  pageSize: number;
  sortBy: string;
  sortOrder: "asc" | "desc";
  filter: string;
  sorting: SortingState;
  setPage: (page: number) => void;
  setFilter: (filter: string) => void;
  setSort: (sortBy: string, sortOrder: "asc" | "desc") => void;
  setSorting: (sorting: SortingState) => void;
}

// useTableState manages page/sort/filter state for a server-side paginated table.
// Filter and sort changes automatically reset to page 1.
// For URL-persisted pagination (QueriesPage, QueryExecutions) compose with
// useSearchNumberState / useSearchState from hooks/use-search-state.ts instead.
export function useTableState(
  options: UseTableStateOptions = {},
): UseTableStateResult {
  const {
    defaultPage = 1,
    defaultPageSize = 10,
    defaultSortBy = "",
    defaultSortOrder = "desc",
    defaultFilter = "",
  } = options;

  const [page, setPageRaw] = useState(defaultPage);
  const [pageSize] = useState(defaultPageSize);
  const [sortBy, setSortByRaw] = useState(defaultSortBy);
  const [sortOrder, setSortOrderRaw] =
    useState<"asc" | "desc">(defaultSortOrder);
  const [filter, setFilterRaw] = useState(defaultFilter);
  const [sorting, setSortingRaw] = useState<SortingState>(
    defaultSortBy
      ? [{ id: defaultSortBy, desc: defaultSortOrder === "desc" }]
      : [],
  );

  const setPage = (p: number) => setPageRaw(p);

  const setFilter = (f: string) => {
    setFilterRaw(f);
    setPageRaw(1);
  };

  const setSort = (by: string, order: "asc" | "desc") => {
    setSortByRaw(by);
    setSortOrderRaw(order);
    setSortingRaw([{ id: by, desc: order === "desc" }]);
    setPageRaw(1);
  };

  const setSorting = (newSorting: SortingState) => {
    setSortingRaw(newSorting);
    if (newSorting.length > 0) {
      setSortByRaw(newSorting[0].id);
      setSortOrderRaw(newSorting[0].desc ? "desc" : "asc");
    }
    setPageRaw(1);
  };

  return {
    page,
    pageSize,
    sortBy,
    sortOrder,
    filter,
    sorting,
    setPage,
    setFilter,
    setSort,
    setSorting,
  };
}
