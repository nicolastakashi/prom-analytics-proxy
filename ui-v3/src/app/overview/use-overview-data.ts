import { useQuery } from "@tanstack/react-query";
import { getQueryTypes, getAverageDuration, getQueryRate } from "@/api/queries";
import { QueryTypesResponse, AverageDurationResponse, QueryRateResponse } from "@/lib/types";
import { DateRange } from "@/lib/types"; // You'll need to create this type

export function useOverviewData(dateRange: DateRange | undefined) {
  const queryEnabled = !!dateRange?.from && !!dateRange?.to;
  const from = dateRange?.from?.toISOString();
  const to = dateRange?.to?.toISOString();

  const { 
    data: queryTypes, 
    isLoading: isLoadingQueryTypes,
    error: queryTypesError 
  } = useQuery<QueryTypesResponse>({
    queryKey: ['queryTypes', from, to],
    queryFn: () => getQueryTypes(from, to),
    enabled: queryEnabled,
  });

  const { 
    data: averageDuration, 
    isLoading: isLoadingAverageDuration,
    error: averageDurationError 
  } = useQuery<AverageDurationResponse>({
    queryKey: ['averageDuration', from, to],
    queryFn: () => getAverageDuration(from, to),
    enabled: queryEnabled,
  });

  const { 
    data: queryRate, 
    isLoading: isLoadingQueryRate,
    error: queryRateError 
  } = useQuery<QueryRateResponse>({
    queryKey: ['queryRate', from, to],
    queryFn: () => getQueryRate(from, to),
    enabled: queryEnabled,
  });

  return {
    data: {
      queryTypes,
      averageDuration,
      queryRate,
    },
    isLoading: isLoadingQueryTypes || isLoadingAverageDuration || isLoadingQueryRate,
    error: queryTypesError || averageDurationError || queryRateError,
  };
} 