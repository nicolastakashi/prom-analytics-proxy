import { renderHook, waitFor } from "@testing-library/react";
import { describe, it, expect, vi } from "vitest";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import React from "react";
import { usePagedQuery } from "./use-paged-query";
import type { PagedResult } from "@/lib/types";

function makeWrapper() {
  const client = new QueryClient({
    defaultOptions: { queries: { retry: false } },
  });
  return ({ children }: { children: React.ReactNode }) =>
    React.createElement(QueryClientProvider, { client }, children);
}

const sampleResult: PagedResult<{ id: number }> = {
  total: 42,
  totalPages: 5,
  data: [{ id: 1 }, { id: 2 }],
};

describe("usePagedQuery", () => {
  it("returns data from queryFn", async () => {
    const queryFn = vi.fn().mockResolvedValue(sampleResult);
    const { result } = renderHook(() => usePagedQuery(["test-key"], queryFn), {
      wrapper: makeWrapper(),
    });

    await waitFor(() => expect(result.current.isSuccess).toBe(true));
    expect(result.current.data).toEqual(sampleResult);
    expect(queryFn).toHaveBeenCalledTimes(1);
  });

  it("passes queryKey through to TanStack Query", async () => {
    const queryFn = vi.fn().mockResolvedValue(sampleResult);
    const key = ["series-metadata", { page: 1, filter: "cpu" }];
    const { result } = renderHook(() => usePagedQuery(key, queryFn), {
      wrapper: makeWrapper(),
    });
    await waitFor(() => expect(result.current.isSuccess).toBe(true));
    expect(queryFn).toHaveBeenCalledTimes(1);
  });

  it("reflects isPending while the query is in-flight", () => {
    const queryFn = vi.fn().mockReturnValue(new Promise(() => {})); // never resolves
    const { result } = renderHook(
      () => usePagedQuery(["pending-key"], queryFn),
      { wrapper: makeWrapper() },
    );
    expect(result.current.isPending).toBe(true);
    expect(result.current.data).toBeUndefined();
  });

  it("reflects isError when queryFn rejects", async () => {
    const queryFn = vi.fn().mockRejectedValue(new Error("network error"));
    const { result } = renderHook(() => usePagedQuery(["error-key"], queryFn), {
      wrapper: makeWrapper(),
    });
    await waitFor(() => expect(result.current.isError).toBe(true));
  });

  it("passes extra options (enabled) through to useQuery", async () => {
    const queryFn = vi.fn().mockResolvedValue(sampleResult);
    const { result } = renderHook(
      () => usePagedQuery(["disabled-key"], queryFn, { enabled: false }),
      { wrapper: makeWrapper() },
    );
    // enabled:false means the query stays idle — queryFn never called
    expect(result.current.isPending).toBe(true);
    expect(queryFn).not.toHaveBeenCalled();
  });

  it("passes staleTime option through to useQuery", async () => {
    const queryFn = vi.fn().mockResolvedValue(sampleResult);
    const client = new QueryClient({
      defaultOptions: { queries: { retry: false } },
    });
    const wrapper = ({ children }: { children: React.ReactNode }) =>
      React.createElement(QueryClientProvider, { client }, children);

    const { result } = renderHook(
      () => usePagedQuery(["stale-key"], queryFn, { staleTime: 60_000 }),
      { wrapper },
    );
    await waitFor(() => expect(result.current.isSuccess).toBe(true));

    // staleTime: data should not be considered stale immediately
    const query = client.getQueryCache().find({ queryKey: ["stale-key"] });
    expect(query?.options.staleTime).toBe(60_000);
  });
});
