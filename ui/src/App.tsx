import { useState } from 'react'
import { Button } from "./components/shadcn/button"
import { Moon, Sun } from "lucide-react"
import { useTheme } from './theme-provider'
import Query from './components/query/query'
import DataTable from './components/datatable/datatable'
import { useQuery } from 'react-query'
import { QueryResult, QueryShortcut } from './fetch'
import fetch from './fetch'
import { useToast } from "./hooks/use-toast"
import { AxiosError } from 'axios'

export default function Component() {
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

  const { setTheme, theme } = useTheme()

  const toggleDarkMode = () => {
    setTheme(theme === 'dark' ? 'light' : 'dark')
  }

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
    <div className={`min-h-screen`}>
      <div className="bg-background text-foreground">
        <header className="border-b border-border">
          <div className="flex items-center justify-between px-4 h-14">
            <div className="flex items-center space-x-3">
              <span className="text-lg font-semibold">Prom Analytics Proxy</span>
            </div>
            <div className="flex items-center">
              <Button variant="ghost" size="icon" onClick={toggleDarkMode}>
                {theme == 'dark' ? <Sun className="h-5 w-5" /> : <Moon className="h-5 w-5" />}
              </Button>
            </div>
          </div>
        </header>

        <div className="flex flex-col min-h-[calc(100vh-3.5rem)]">
          <Query
            isLoading={isQueryResultLoading}
            query={query}
            queryShortcuts={queryShortcuts || []}
            handleQueryChange={handleQueryChange}
            handleShortcutClick={handleShortcutClick}
            handleExecuteQuery={handleExecuteQuery}
          />
          <DataTable result={data || { columns: [], data: [] }} />
        </div>
      </div>
    </div>
  )
}


