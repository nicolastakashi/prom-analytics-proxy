import { renderHook, act } from "@testing-library/react";
import { describe, it, expect } from "vitest";
import { useTableState } from "@/hooks/use-table-state";

describe("tab pagination state (useTableState)", () => {
  it("resets page to 1 when filter changes", () => {
    const { result } = renderHook(() => useTableState({ defaultPage: 5 }));
    act(() => result.current.setFilter("cpu"));
    expect(result.current.page).toBe(1);
  });

  it("resets page to 1 when sort column changes via setSorting", () => {
    const { result } = renderHook(() => useTableState({ defaultPage: 5 }));
    act(() => result.current.setSorting([{ id: "name", desc: false }]));
    expect(result.current.page).toBe(1);
  });

  it("resets page to 1 when sort column changes via setSort", () => {
    const { result } = renderHook(() => useTableState({ defaultPage: 5 }));
    act(() => result.current.setSort("name", "asc"));
    expect(result.current.page).toBe(1);
  });

  it("preserves page when only the page number changes", () => {
    const { result } = renderHook(() => useTableState({ defaultPage: 1 }));
    act(() => result.current.setPage(4));
    expect(result.current.page).toBe(4);
  });

  it("initialises sorting state from defaultSortBy and defaultSortOrder", () => {
    const { result } = renderHook(() =>
      useTableState({ defaultSortBy: "avgDuration", defaultSortOrder: "desc" }),
    );
    expect(result.current.sorting).toEqual([{ id: "avgDuration", desc: true }]);
    expect(result.current.sortBy).toBe("avgDuration");
    expect(result.current.sortOrder).toBe("desc");
  });
});
