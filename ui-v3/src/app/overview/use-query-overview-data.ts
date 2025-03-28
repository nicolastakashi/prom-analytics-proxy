import { getQueryLatencyTrends, getQueryStatusDistribution, getQueryThroughputAnalysis } from "@/api/queries";
import { DateRange, QueryLatencyTrendsResult, QueryStatusDistributionResult, QueryThroughputAnalysisResult } from "@/lib/types";
import { useQuery } from "@tanstack/react-query";

interface QueryOverviewData {
    queryStatusDistribution: QueryStatusDistributionResult[] | undefined;
    queryLatencyTrends: QueryLatencyTrendsResult[] | undefined;
    queryThroughputAnalysis: QueryThroughputAnalysisResult[] | undefined;
}

export function useQueryOverviewData(dateRange: DateRange | undefined) {
    const queryEnabled = Boolean(dateRange?.from && dateRange?.to);
    const from = dateRange?.from?.toISOString();
    const to = dateRange?.to?.toISOString();

    const { 
        data: queryStatusDistribution,
        isLoading: isLoadingStatus,
        error: errorStatus
    } = useQuery<QueryStatusDistributionResult[]>({
        queryKey: ['queryStatusDistribution', from, to],
        queryFn: () => getQueryStatusDistribution(from, to),
        enabled: queryEnabled,
    });

    const {
        data: queryLatencyTrends,
        isLoading: isLoadingLatency,
        error: errorLatency
    } = useQuery<QueryLatencyTrendsResult[]>({
        queryKey: ['queryLatencyTrends', from, to],
        queryFn: () => getQueryLatencyTrends(from, to),
        enabled: queryEnabled,
    });

    const {
        data: queryThroughputAnalysis,
        isLoading: isLoadingThroughput,
        error: errorThroughput
    } = useQuery<QueryThroughputAnalysisResult[]>({
        queryKey: ['queryThroughputAnalysis', from, to],
        queryFn: () => getQueryThroughputAnalysis(from, to),
        enabled: queryEnabled,
    });

    return {
        data: {
            queryStatusDistribution,
            queryLatencyTrends,
            queryThroughputAnalysis,
        } as QueryOverviewData,
        isLoading: isLoadingStatus || isLoadingLatency || isLoadingThroughput,
        error: errorStatus || errorLatency || errorThroughput,
    };
}
