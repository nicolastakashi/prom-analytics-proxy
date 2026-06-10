import { renderHook, waitFor } from "@testing-library/react";
import { describe, it, expect, vi, beforeEach } from "vitest";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import React from "react";
import { useSeriesMetadataTable, useMetricUsage } from "./use-metrics-data";
import * as metricsApi from "@/api/metrics";

vi.mock("@/api/metrics");

beforeEach(() => {
  vi.clearAllMocks();
});

function makeWrapper() {
  const client = new QueryClient({
    defaultOptions: { queries: { retry: false } },
  });
  return ({ children }: { children: React.ReactNode }) =>
    React.createElement(QueryClientProvider, { client }, children);
}

const pagedMetrics = {
  total: 2,
  totalPages: 1,
  data: [
    { name: "up", type: "gauge", help: "", unit: "" },
    { name: "scrape_duration_seconds", type: "gauge", help: "", unit: "" },
  ],
};

const pagedUsage = {
  total: 3,
  totalPages: 1,
  data: [
    { name: "alert-rule-1", expression: "up == 0" },
    { name: "alert-rule-2", expression: "rate(errors[5m]) > 0" },
    { name: "alert-rule-3", expression: "up < 1" },
  ],
};

describe("useSeriesMetadataTable", () => {
  beforeEach(() => {
    vi.mocked(metricsApi.getSeriesMetadata).mockResolvedValue(pagedMetrics);
    vi.mocked(metricsApi.getProducers).mockResolvedValue([]);
  });

  it("calls getSeriesMetadata with page and pageSize from tableState", async () => {
    const tableState = {
      page: 2,
      pageSize: 20,
      sortBy: "queryCount",
      sortOrder: "desc" as const,
      filter: "",
      type: "all",
    };
    const { result } = renderHook(() => useSeriesMetadataTable(tableState), {
      wrapper: makeWrapper(),
    });
    await waitFor(() => expect(result.current.isLoading).toBe(false));
    expect(metricsApi.getSeriesMetadata).toHaveBeenCalledWith(
      2,
      20,
      "queryCount",
      "desc",
      "",
      "all",
      "all",
      undefined,
    );
  });

  it("calls getSeriesMetadata with sortBy and sortOrder from tableState", async () => {
    const tableState = {
      page: 1,
      pageSize: 10,
      sortBy: "alertCount",
      sortOrder: "asc" as const,
      filter: "",
      type: "gauge",
    };
    renderHook(() => useSeriesMetadataTable(tableState), {
      wrapper: makeWrapper(),
    });
    await waitFor(() =>
      expect(metricsApi.getSeriesMetadata).toHaveBeenCalledWith(
        1,
        10,
        "alertCount",
        "asc",
        "",
        "gauge",
        "all",
        undefined,
      ),
    );
  });

  it("passes searchQuery as the filter argument", async () => {
    const tableState = {
      page: 1,
      pageSize: 10,
      sortBy: "name",
      sortOrder: "asc" as const,
      filter: "",
      type: "all",
    };
    renderHook(() => useSeriesMetadataTable(tableState, "prom"), {
      wrapper: makeWrapper(),
    });
    await waitFor(() =>
      expect(metricsApi.getSeriesMetadata).toHaveBeenCalledWith(
        1,
        10,
        "name",
        "asc",
        "prom",
        "all",
        "all",
        undefined,
      ),
    );
  });

  it("passes usage filter to getSeriesMetadata", async () => {
    const tableState = {
      page: 1,
      pageSize: 10,
      sortBy: "name",
      sortOrder: "asc" as const,
      filter: "",
      type: "all",
    };
    renderHook(() => useSeriesMetadataTable(tableState, "", "unused"), {
      wrapper: makeWrapper(),
    });
    await waitFor(() =>
      expect(metricsApi.getSeriesMetadata).toHaveBeenCalledWith(
        1,
        10,
        "name",
        "asc",
        "",
        "all",
        "unused",
        undefined,
      ),
    );
  });

  it("returns PagedResult data", async () => {
    const tableState = {
      page: 1,
      pageSize: 10,
      sortBy: "name",
      sortOrder: "asc" as const,
      filter: "",
      type: "all",
    };
    const { result } = renderHook(() => useSeriesMetadataTable(tableState), {
      wrapper: makeWrapper(),
    });
    await waitFor(() => expect(result.current.isLoading).toBe(false));
    expect(result.current.data.metrics?.total).toBe(2);
    expect(result.current.data.metrics?.data).toHaveLength(2);
    expect(result.current.data.metrics?.data[0].name).toBe("up");
  });
});

describe("useMetricUsage", () => {
  beforeEach(() => {
    vi.mocked(metricsApi.getMetricUsage).mockResolvedValue(pagedUsage);
  });

  it("calls getMetricUsage with the right params", async () => {
    const { result } = renderHook(
      () => useMetricUsage("my_metric", "alert", 1, 10),
      { wrapper: makeWrapper() },
    );
    await waitFor(() => expect(result.current.isSuccess).toBe(true));
    expect(metricsApi.getMetricUsage).toHaveBeenCalledWith(
      "my_metric",
      "alert",
      1,
      10,
      "",
      "",
    );
  });

  it("is disabled when metricName is empty", () => {
    const { result } = renderHook(() => useMetricUsage("", "alert"), {
      wrapper: makeWrapper(),
    });
    expect(result.current.isPending).toBe(true);
    expect(metricsApi.getMetricUsage).not.toHaveBeenCalled();
  });

  it("is disabled when kind is empty", () => {
    const { result } = renderHook(() => useMetricUsage("my_metric", ""), {
      wrapper: makeWrapper(),
    });
    expect(result.current.isPending).toBe(true);
    expect(metricsApi.getMetricUsage).not.toHaveBeenCalled();
  });

  it("returns PagedResult data", async () => {
    const { result } = renderHook(() => useMetricUsage("my_metric", "record"), {
      wrapper: makeWrapper(),
    });
    await waitFor(() => expect(result.current.isSuccess).toBe(true));
    expect(result.current.data?.total).toBe(3);
    expect(result.current.data?.data).toHaveLength(3);
  });

  it("re-fetches when page changes", async () => {
    const { result, rerender } = renderHook(
      ({ page }: { page: number }) =>
        useMetricUsage("my_metric", "alert", page, 10),
      { wrapper: makeWrapper(), initialProps: { page: 1 } },
    );
    await waitFor(() => expect(result.current.isSuccess).toBe(true));
    expect(metricsApi.getMetricUsage).toHaveBeenCalledWith(
      "my_metric",
      "alert",
      1,
      10,
      "",
      "",
    );

    rerender({ page: 2 });
    await waitFor(() =>
      expect(metricsApi.getMetricUsage).toHaveBeenCalledWith(
        "my_metric",
        "alert",
        2,
        10,
        "",
        "",
      ),
    );
  });
});
