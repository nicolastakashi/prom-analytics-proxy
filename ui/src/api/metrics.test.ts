import { afterEach, describe, expect, it, vi } from "vitest";
import { getProducerStats } from "./metrics";

describe("getProducerStats", () => {
  afterEach(() => {
    vi.unstubAllGlobals();
  });

  it("requests paginated producer statistics with search and sorting", async () => {
    const response = {
      total: 1,
      totalPages: 1,
      data: [
        {
          job: "node-exporter",
          metricCount: 42,
          usedMetricCount: 30,
          unusedMetricCount: 12,
          contribution: 25,
        },
      ],
    };
    const fetchMock = vi.fn().mockResolvedValue({
      ok: true,
      json: vi.fn().mockResolvedValue(response),
    });
    vi.stubGlobal("fetch", fetchMock);

    await expect(
      getProducerStats(2, 25, "unusedMetricCount", "asc", "node"),
    ).resolves.toEqual(response);
    expect(fetchMock).toHaveBeenCalledWith(
      "/api/v1/producers?page=2&pageSize=25&sortBy=unusedMetricCount&sortOrder=asc&filter=node",
    );
  });
});
