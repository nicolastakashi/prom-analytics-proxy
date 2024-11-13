import { useState } from 'react'
import Query from '../../components/query/query'
import DataTable from '../../components/datatable/datatable'
import { useQuery } from 'react-query'
import { QueryResult, QueryShortcut } from '../../fetch'
import fetch from '../../fetch'
import { useToast } from "../../hooks/use-toast"
import { AxiosError } from 'axios'

export default function Explore() {
    const [query, setQuery] = useState('')
    const { toast } = useToast()

    const errorHandler = (error: unknown) => {
        let description = error instanceof Error ? error.message : "An unknown error occurred"
        if (error instanceof AxiosError) {
            description = error.response?.data || "An unknown error occurred"
        }

        toast({
            variant: "destructive",
            title: "Uh oh! Something went wrong.",
            description: description,
        })
    }

    const { data, isLoading: isQueryResultLoading, refetch } = useQuery<QueryResult>(
        ['analyticsData'],
        () => fetch.Queries(query),
        {
            enabled: false,
            onError: errorHandler,
        }
    );

    const { data: queryShortcuts } = useQuery<QueryShortcut[]>(
        ['queryShortcuts'],
        () => fetch.QueryShortcuts(),
        {
            onError: errorHandler,
        }
    );

    const handleExecuteQuery = () => {
        refetch()
    }

    const handleShortcutClick = (shortcutQuery: string) => {
        setQuery(shortcutQuery)
    }

    const handleQueryChange = (value: string) => {
        setQuery(value)
    }

    return (
        <>
            <Query
                isLoading={isQueryResultLoading}
                query={query}
                queryShortcuts={queryShortcuts || []}
                handleQueryChange={handleQueryChange}
                handleShortcutClick={handleShortcutClick}
                handleExecuteQuery={handleExecuteQuery}
            />
            <DataTable result={data || { columns: [], data: [] }} />
        </>
    )
}


