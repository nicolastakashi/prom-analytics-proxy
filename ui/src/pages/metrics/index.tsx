import { useState, useMemo, Dispatch, SetStateAction } from 'react';
import { Input } from "../../components/shadcn/input";
import { Badge } from "../../components/shadcn/badge";
import { Button } from "../../components/shadcn/button";
import { Card, CardContent, CardHeader, CardTitle } from "../../components/shadcn/card";
import { Tooltip, TooltipContent, TooltipProvider, TooltipTrigger } from "../../components/shadcn/tooltip";
import { ChevronLeft, ChevronRight, AlertCircle } from 'lucide-react';
import { useQuery } from 'react-query';
import fetch, { SeriesMetadata } from '../../fetch';
import { AxiosError } from 'axios';
import { toast } from '../../hooks/use-toast';
import { useNavigate } from 'react-router-dom';
import Progress from '../../components/progress/progress';

const ITEMS_PER_PAGE = 12;
const MAX_DESCRIPTION_LENGTH = 100;

const errorHandler = (error: unknown) => {
    const description = error instanceof AxiosError ? error.response?.data || "An unknown error occurred" : error instanceof Error ? error.message : "An unknown error occurred";
    toast({ variant: "destructive", title: "Uh oh! Something went wrong.", description });
};

const Component = () => {
    const navigate = useNavigate();
    const [currentPage, setCurrentPage] = useState<number>(1);
    const [searchTerm, setSearchTerm] = useState<string>('');

    const { data, isLoading } = useQuery<SeriesMetadata[]>(['seriesMetadata'], fetch.GetSeriesMetadata, { onError: errorHandler });

    const filteredMetrics = useMemo(
        () => data?.filter(metric => metric.name.toLowerCase().includes(searchTerm.toLowerCase()) || metric.help.toLowerCase().includes(searchTerm.toLowerCase())),
        [data, searchTerm]
    );

    const paginatedMetrics = useMemo(() => {
        const startIndex = (currentPage - 1) * ITEMS_PER_PAGE;
        return filteredMetrics?.slice(startIndex, startIndex + ITEMS_PER_PAGE);
    }, [filteredMetrics, currentPage]);

    const totalPages = Math.ceil((filteredMetrics?.length ?? 0) / ITEMS_PER_PAGE);
    const startIndex = (currentPage - 1) * ITEMS_PER_PAGE + 1;
    const endIndex = Math.min(startIndex + (paginatedMetrics?.length ?? 0) - 1, filteredMetrics?.length ?? 0);

    const handlePageChange = (direction: 'next' | 'prev') => {
        setCurrentPage(prev => direction === 'next' ? Math.min(prev + 1, totalPages) : Math.max(prev - 1, 1));
    };

    const truncateDescription = (description: string) => description.length > MAX_DESCRIPTION_LENGTH ? description.slice(0, MAX_DESCRIPTION_LENGTH) + '...' : description;

    const navigateToMetricDetails = (metric: SeriesMetadata) => {
        navigate(`/metrics/${metric.name}`, { state: { metric } });
    }

    return (
        <>
            <Progress isAnimating={isLoading} />
            <div className="p-6">
                <div className="flex flex-col space-y-4">
                    <Header searchTerm={searchTerm} setSearchTerm={setSearchTerm} setCurrentPage={setCurrentPage} />
                    {filteredMetrics?.length ? (
                        <>
                            <MetricGrid metrics={paginatedMetrics} truncateDescription={truncateDescription} onClick={navigateToMetricDetails} />
                            <Pagination currentPage={currentPage} totalPages={totalPages} startIndex={startIndex} endIndex={endIndex} totalItems={filteredMetrics.length} onPageChange={handlePageChange} />
                        </>
                    ) : <NoMetricsFound />}
                </div>
            </div>
        </>
    );
};

type HeaderProps = {
    searchTerm: string;
    setSearchTerm: Dispatch<SetStateAction<string>>;
    setCurrentPage: Dispatch<SetStateAction<number>>;
};

const Header = ({ searchTerm, setSearchTerm, setCurrentPage }: HeaderProps) => (
    <div className="flex justify-between items-center">
        <div className="space-y-1">
            <h1 className="text-2xl font-semibold tracking-tight">Metrics</h1>
            <p className="text-sm text-muted-foreground">Browse and search all available metrics</p>
        </div>
        <Input placeholder="Search metrics" value={searchTerm} onChange={(e) => { setSearchTerm(e.target.value); setCurrentPage(1); }} className="w-full max-w-sm" />
    </div>
);

type MetricGridProps = {
    metrics: SeriesMetadata[] | undefined;
    truncateDescription: (description: string) => string;
    onClick: (metric: SeriesMetadata) => void;
};

const MetricGrid = ({ metrics, truncateDescription, onClick }: MetricGridProps) => (
    <div className="grid gap-4 md:grid-cols-2 lg:grid-cols-3 cursor-pointer">
        {metrics?.map(metric => (
            <Card key={metric.name} className="flex flex-col min-h-[140px] transition-shadow hover:shadow-md" onClick={() => onClick(metric)}>
                <CardHeader className="pb-2 pt-4 px-4 space-y-3">
                    <div className="flex justify-between center gap-2 mt-1">
                        <CardTitle className="font-medium text-sm leading-tight break-all">{metric.name}</CardTitle>
                        <Badge variant="secondary" className="shrink-0">{metric.type}</Badge>
                    </div>
                    <TooltipProvider>
                        <Tooltip>
                            <TooltipTrigger asChild>
                                <p className="text-sm text-muted-foreground line-clamp-2">{truncateDescription(metric.help)}</p>
                            </TooltipTrigger>
                            <TooltipContent><p className="max-w-xs">{metric.help}</p></TooltipContent>
                        </Tooltip>
                    </TooltipProvider>
                </CardHeader>
                <CardContent className="mt-auto" />
            </Card>
        ))}
    </div>
);

type PaginationProps = {
    currentPage: number;
    totalPages: number;
    startIndex: number;
    endIndex: number;
    totalItems: number;
    onPageChange: (direction: 'next' | 'prev') => void;
};

const Pagination = ({ currentPage, totalPages, startIndex, endIndex, totalItems, onPageChange }: PaginationProps) => (
    <div className="flex items-center justify-between">
        <p className="text-sm text-muted-foreground">Showing {startIndex} to {endIndex} of {totalItems} metrics</p>
        <div className="flex items-center space-x-2">
            <Button variant="outline" size="sm" onClick={() => onPageChange('prev')} disabled={currentPage === 1}>
                <ChevronLeft className="h-4 w-4 mr-1" />Previous
            </Button>
            <div className="text-sm font-medium">Page {currentPage} of {totalPages}</div>
            <Button variant="outline" size="sm" onClick={() => onPageChange('next')} disabled={currentPage === totalPages}>
                Next<ChevronRight className="h-4 w-4 ml-1" />
            </Button>
        </div>
    </div>
);

const NoMetricsFound = () => (
    <Card className="flex flex-col items-center justify-center p-4 text-center min-h-[140px]">
        <AlertCircle className="h-10 w-10 text-muted-foreground mb-4" />
        <CardTitle className="text-xl font-semibold mb-2">No metrics found</CardTitle>
        <CardContent>
            <p className="text-muted-foreground">There are no metrics matching your search criteria. Try adjusting your search or check back later.</p>
        </CardContent>
    </Card>
);

export default Component;