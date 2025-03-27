import { getQueryLatencyTrends, getQueryStatusDistribution } from "@/api/queries";
import { DateRange, QueryLatencyTrendsResult, QueryStatusDistributionResult } from "@/lib/types";
import { useQuery } from "@tanstack/react-query";

export function useQueryOverviewData(dateRange: DateRange | undefined) {
    const queryEnabled = !!dateRange?.from && !!dateRange?.to;
    const from = dateRange?.from?.toISOString();
    const to = dateRange?.to?.toISOString();
    
    const { data: queryStatusDistribution, isLoading, error } = useQuery<QueryStatusDistributionResult[]>({
        queryKey: ['queryStatusDistribution', from, to],
        queryFn: () => getQueryStatusDistribution(from, to),
        enabled: queryEnabled,
    });

    const { data: queryLatencyTrends, isLoading: isLoadingLatencyTrends, error: errorLatencyTrends } = useQuery<QueryLatencyTrendsResult[]>({
        queryKey: ['queryLatencyTrends', from, to],
        queryFn: () => getQueryLatencyTrends(from, to),
        enabled: queryEnabled,
    });

    return {
        data: {
            queryStatusDistribution,
            queryLatencyTrends,
        },
        isLoading: isLoading || isLoadingLatencyTrends,
        error: error || errorLatencyTrends,
    };
}
