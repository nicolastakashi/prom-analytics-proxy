import { Tag, Info, BookOpen, Clock } from 'lucide-react'
import { Badge } from "../../components/shadcn/badge"
import { useLocation } from 'react-router-dom'
import fetch, { PagedResult, SerieExpression, SerieMetadata } from '../../fetch';
import { useQuery } from 'react-query'
import { AxiosError } from 'axios'
import { toast } from '../../hooks/use-toast'
import Progress from '../../components/progress/progress'
import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from '../../components/shadcn/table';
import { Tabs, TabsContent, TabsList, TabsTrigger } from '../../components/shadcn/tabs';
import formatDuration from 'format-duration';
import { EditorView } from '@uiw/react-codemirror';
import { PromQLExtension, } from '@prometheus-io/codemirror-promql';
import CodeMirror from '@uiw/react-codemirror';
import { darkPromqlHighlighter } from '../../components/query/query';
import { useTheme } from '../../theme-provider';
import './detail.css'
import { Pagination } from '.';
import { useState } from 'react';

const errorHandler = (error: unknown) => {
    const description = error instanceof AxiosError ? error.response?.data || "An unknown error occurred" : error instanceof Error ? error.message : "An unknown error occurred";
    toast({ variant: "destructive", title: "Uh oh! Something went wrong.", description });
};


const promqlExtension = new PromQLExtension();

const formatSeriesCount = (count: number | undefined) => {
    if (!count) {
        return 0
    }

    if (count < 5000) {
        return count
    }
    return count + '+'
}


interface ExpressionTableProps {
    expressions: PagedResult<SerieExpression> | undefined;
    currentPage: number;
    onPageChange: (direction: 'next' | 'prev') => void;
}

const ExpressionTable: React.FC<ExpressionTableProps> = ({ expressions, currentPage, onPageChange }) => {
    const { theme } = useTheme();
    const startIndex = (currentPage - 1) * 1 + 1;
    const endIndex = Math.min(startIndex + (expressions?.data.length ?? 0) - 1, expressions?.data.length ?? 0);

    return (
        <TabsContent value="expressions" className="p-4">
            <Table>
                <TableHeader>
                    <TableRow>
                        <TableHead className="w-2/5">Expression</TableHead>
                        <TableHead>Avg Duration</TableHead>
                        <TableHead>Avg PeakySamples</TableHead>
                        <TableHead>Max PeakSamples</TableHead>
                    </TableRow>
                </TableHeader>
                <TableBody>
                    {expressions?.data.map((expr, index) => (
                        <TableRow key={index}>
                            <TableCell className="font-mono text-sm">
                                <CodeMirror
                                    readOnly={true}
                                    value={expr.queryParam}
                                    extensions={[EditorView.lineWrapping, promqlExtension.asExtension()]}
                                    height="100%"
                                    inputMode={'none'}
                                    editable={false}
                                    theme={theme === 'dark' ? darkPromqlHighlighter : 'light'}
                                    basicSetup={{
                                        highlightActiveLine: false,
                                        highlightActiveLineGutter: false,
                                        foldGutter: false,
                                        lineNumbers: false,
                                        history: true,
                                        autocompletion: true,
                                        syntaxHighlighting: true,
                                        allowMultipleSelections: true,
                                        highlightSelectionMatches: true,
                                        highlightSpecialChars: true,
                                        closeBrackets: true,
                                        bracketMatching: true,
                                        indentOnInput: true,
                                        closeBracketsKeymap: true,
                                        defaultKeymap: true,
                                        historyKeymap: true,
                                        completionKeymap: true,
                                        lintKeymap: true,
                                    }}
                                    className='hover:bg-muted/50'
                                />
                            </TableCell>
                            <TableCell>{formatDuration(expr.avgDuration, { ms: true })}</TableCell>
                            <TableCell>{expr.avgPeakySamples}</TableCell>
                            <TableCell>{expr.maxPeakSamples}</TableCell>
                        </TableRow>
                    ))}
                </TableBody>
            </Table>
            <div className='pt-1'>
                <Pagination
                    currentPage={currentPage}
                    totalPages={expressions?.totalPages || 0}
                    startIndex={startIndex}
                    endIndex={endIndex}
                    totalItems={expressions?.data.length || 0}
                    onPageChange={onPageChange}
                />
            </div>
        </TabsContent>
    );
};

export default function Component() {
    const location = useLocation();

    const { metric } = location.state
    const [currentExpressionsPage, setCurrentExpressionsPage] = useState<number>(1);

    const { data: serieMetadata, isLoading: isLoadingSerieMetadata } = useQuery<SerieMetadata>(
        ['seriesMetadata', metric.name],
        () => fetch.GetSerieMetadata(metric.name),
        { onError: errorHandler }
    );

    const { data: expressions, isLoading: isLoadingExpressions } = useQuery<PagedResult<SerieExpression>>(
        ['serieExpressions', metric.name, currentExpressionsPage],
        () => fetch.GetSerieExpressions(metric.name, currentExpressionsPage),
        { onError: errorHandler }
    );

    const handleExpressionsPageChange = (direction: 'next' | 'prev') => {
        setCurrentExpressionsPage(prev => direction === 'next' ? Math.min(prev + 1, expressions?.totalPages || 0) : Math.max(prev - 1, 1));
    };

    return (
        <>
            <Progress isAnimating={isLoadingSerieMetadata || isLoadingExpressions} />
            <div className="flex flex-col min-h-screen bg-background">
                <main className="grow p-6">
                    <div className="mb-6">
                        <h2 className="text-2xl font-bold text-foreground mb-2">{metric.name}</h2>
                        <p className="text-muted-foreground">{metric.help}</p>
                    </div>
                    <div className="grid gap-6 md:grid-cols-2 mb-6">
                        <div className="bg-card p-4 rounded-lg shadow border border-border">
                            <h3 className="text-lg font-semibold text-foreground mb-4 flex items-center">
                                <Info className="mr-2 h-5 w-5 text-muted-foreground" />
                                Overview
                            </h3>
                            <dl className="grid gap-2">
                                <div className="flex justify-between items-center">
                                    <dt className="font-medium text-muted-foreground">Type:</dt>
                                    <dd>
                                        {metric && <Badge variant="outline" className="text-xs">
                                            {metric.type}
                                        </Badge>}
                                    </dd>
                                </div>
                                <div className="flex justify-between">
                                    <dt className="font-medium text-muted-foreground">Number of series:</dt>
                                    <dd className="text-foreground">
                                        <Badge variant='outline' className="text-xs">
                                            {formatSeriesCount(serieMetadata?.seriesCount)}
                                        </Badge>

                                    </dd>
                                </div>
                            </dl>
                        </div>
                        <div className="bg-card p-4 rounded-lg shadow border border-border">
                            <h3 className="text-lg font-semibold text-foreground mb-4 flex items-center">
                                <Tag className="mr-2 h-5 w-5 text-muted-foreground" />
                                Labels
                            </h3>
                            <div className="flex flex-wrap gap-2">
                                {serieMetadata?.labels.filter(label => label != "__name__").map((label) => {
                                    return (
                                        <Badge key={label} variant="secondary" className="text-foreground bg-secondary">
                                            {label}
                                        </Badge>
                                    )
                                })}
                            </div>
                        </div>
                    </div>
                    <div className="bg-card rounded-lg shadow">
                        <div className="p-4 border-b border-border">
                            <h3 className="text-lg font-semibold text-foreground flex items-center">
                                <BookOpen className="mr-2 h-5 w-5 text-muted-foreground" />
                                Metric Usage
                            </h3>
                        </div>
                        <Tabs defaultValue="expressions" className="w-full">
                            <TabsList className="grid w-full grid-cols-1">
                                <TabsTrigger value="expressions" className="flex items-center justify-center">
                                    <Clock className="mr-2 h-4 w-4" />
                                    <span className="hidden sm:inline">Expressions</span>
                                    <Badge variant="secondary" className="ml-2">
                                        {expressions?.data.length}
                                    </Badge>
                                </TabsTrigger>
                            </TabsList>
                            <ExpressionTable expressions={expressions} currentPage={currentExpressionsPage} onPageChange={handleExpressionsPageChange} />
                        </Tabs>
                    </div >
                </main >
            </div >
        </>
    )
}