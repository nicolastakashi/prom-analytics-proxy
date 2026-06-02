import { renderHook, act } from "@testing-library/react";
import { describe, it, expect } from "vitest";
import { useTableState } from "./use-table-state";

describe("useTableState", () => {
  describe("initial state", () => {
    it("uses library defaults when no options given", () => {
      const { result } = renderHook(() => useTableState());
      expect(result.current.page).toBe(1);
      expect(result.current.pageSize).toBe(10);
      expect(result.current.sortBy).toBe("");
      expect(result.current.sortOrder).toBe("desc");
      expect(result.current.filter).toBe("");
      expect(result.current.sorting).toEqual([]);
    });

    it("uses provided options", () => {
      const { result } = renderHook(() =>
        useTableState({
          defaultPage: 3,
          defaultPageSize: 25,
          defaultSortBy: "name",
          defaultSortOrder: "asc",
          defaultFilter: "prometheus",
        }),
      );
      expect(result.current.page).toBe(3);
      expect(result.current.pageSize).toBe(25);
      expect(result.current.sortBy).toBe("name");
      expect(result.current.sortOrder).toBe("asc");
      expect(result.current.filter).toBe("prometheus");
    });

    it("derives initial SortingState from defaultSortBy + defaultSortOrder", () => {
      const { result } = renderHook(() =>
        useTableState({ defaultSortBy: "executions", defaultSortOrder: "desc" }),
      );
      expect(result.current.sorting).toEqual([{ id: "executions", desc: true }]);
    });

    it("produces empty SortingState when no defaultSortBy", () => {
      const { result } = renderHook(() => useTableState({ defaultSortOrder: "asc" }));
      expect(result.current.sorting).toEqual([]);
    });
  });

  describe("setPage", () => {
    it("updates page without touching other state", () => {
      const { result } = renderHook(() =>
        useTableState({ defaultPage: 1, defaultFilter: "foo", defaultSortBy: "name" }),
      );
      act(() => result.current.setPage(4));
      expect(result.current.page).toBe(4);
      expect(result.current.filter).toBe("foo");
      expect(result.current.sortBy).toBe("name");
    });
  });

  describe("setFilter", () => {
    it("updates filter and resets page to 1", () => {
      const { result } = renderHook(() => useTableState({ defaultPage: 5 }));
      act(() => result.current.setFilter("my-metric"));
      expect(result.current.filter).toBe("my-metric");
      expect(result.current.page).toBe(1);
    });

    it("resets page to 1 even when filter is cleared", () => {
      const { result } = renderHook(() =>
        useTableState({ defaultPage: 3, defaultFilter: "something" }),
      );
      act(() => result.current.setFilter(""));
      expect(result.current.filter).toBe("");
      expect(result.current.page).toBe(1);
    });
  });

  describe("setSort", () => {
    it("updates sortBy, sortOrder, and SortingState together", () => {
      const { result } = renderHook(() => useTableState());
      act(() => result.current.setSort("avgDuration", "asc"));
      expect(result.current.sortBy).toBe("avgDuration");
      expect(result.current.sortOrder).toBe("asc");
      expect(result.current.sorting).toEqual([{ id: "avgDuration", desc: false }]);
    });

    it("resets page to 1", () => {
      const { result } = renderHook(() => useTableState({ defaultPage: 7 }));
      act(() => result.current.setSort("name", "desc"));
      expect(result.current.page).toBe(1);
    });
  });

  describe("setSorting", () => {
    it("syncs sortBy and sortOrder from SortingState array", () => {
      const { result } = renderHook(() => useTableState());
      act(() => result.current.setSorting([{ id: "peakSamples", desc: false }]));
      expect(result.current.sortBy).toBe("peakSamples");
      expect(result.current.sortOrder).toBe("asc");
      expect(result.current.sorting).toEqual([{ id: "peakSamples", desc: false }]);
    });

    it("resets page to 1", () => {
      const { result } = renderHook(() => useTableState({ defaultPage: 3 }));
      act(() => result.current.setSorting([{ id: "name", desc: true }]));
      expect(result.current.page).toBe(1);
    });

    it("keeps sortBy/sortOrder unchanged when given empty array, but still resets page", () => {
      const { result } = renderHook(() =>
        useTableState({ defaultPage: 2, defaultSortBy: "name", defaultSortOrder: "asc" }),
      );
      act(() => result.current.setSorting([]));
      expect(result.current.sortBy).toBe("name");
      expect(result.current.sortOrder).toBe("asc");
      expect(result.current.sorting).toEqual([]);
      expect(result.current.page).toBe(1);
    });
  });
});
