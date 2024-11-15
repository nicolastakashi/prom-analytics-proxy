import { Tag, Info } from 'lucide-react'
import { Badge } from "../../components/shadcn/badge"
import { useLocation } from 'react-router-dom'
import fetch from '../../fetch';
import { useQuery } from 'react-query'
import { AxiosError } from 'axios'
import { toast } from '../../hooks/use-toast'
import Progress from '../../components/progress/progress'

const errorHandler = (error: unknown) => {
    const description = error instanceof AxiosError ? error.response?.data || "An unknown error occurred" : error instanceof Error ? error.message : "An unknown error occurred";
    toast({ variant: "destructive", title: "Uh oh! Something went wrong.", description });
};

export default function Component() {
    const location = useLocation();
    const { metric } = location.state

    const { data: labels, isLoading } = useQuery<string[]>(['seriesMetadata', metric.name], () => fetch.GetSerieLabels(metric.name), { onError: errorHandler });

    return (
        <>
            <Progress isAnimating={isLoading} />
            <div className="flex flex-col min-h-screen bg-background">
                <main className="grow p-6">
                    <div className="mb-6">
                        <h2 className="text-2xl font-bold text-foreground mb-2">{metric.name}</h2>
                        <p className="text-muted-foreground">{metric.help}</p>
                    </div>

                    <div className="grid gap-6 md:grid-cols-2 mb-6">
                        <div className="bg-card p-4 rounded-lg shadow">
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
                                    <dt className="font-medium text-muted-foreground">Time Series:</dt>
                                    <dd className="text-foreground">{100}</dd>
                                </div>
                            </dl>
                        </div>

                        <div className="bg-card p-4 rounded-lg shadow">
                            <h3 className="text-lg font-semibold text-foreground mb-4 flex items-center">
                                <Tag className="mr-2 h-5 w-5 text-muted-foreground" />
                                Labels
                            </h3>
                            <div className="flex flex-wrap gap-2">
                                {labels && labels.map((label) => {
                                    return (
                                        <Badge key={label} variant="secondary" className="text-foreground bg-secondary">
                                            {label}
                                        </Badge>
                                    )
                                })}
                            </div>
                        </div>
                    </div>
                </main>
            </div>
        </>
    )
}