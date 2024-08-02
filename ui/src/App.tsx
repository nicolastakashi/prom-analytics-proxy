import { useState } from 'react';
import Header from './components/Header';
import CodeEditor from './components/CodeEditor';
import Table, { Result } from './components/Table';
import { useQuery } from 'react-query';
import fetchAnalyticsData from './fetch';

const schema = {
  queries: [
    'TS',
    'Fingerprint',
    'QueryParam',
    'TimeParam',
    'LabelMatchers.Key',
    'LabelMatchers.Value',
    'Duration',
    'StatusCode',
    'BodySize'
  ]
}

function App() {
  const [query, setQuery] = useState('');
  const { data, error, isLoading, refetch } = useQuery<Result>(
    ['analyticsData', query],
    () => fetchAnalyticsData(query),
    {
      enabled: false, // Only run the query if the query is not empty
    }
  );

  const handleRunQuery = () => {
    refetch();
  };

  return (
    <div className="min-h-screen flex flex-col bg-gray-50">
      <Header />
      <main className="flex-grow p-4">
        <div className="flex flex-col h-full">
          <CodeEditor
            value={query}
            onChange={setQuery}
            onSubmit={handleRunQuery}
            schema={schema}
            isLoading={isLoading}
          />
          <div className="flex-grow mt-4">
            {isLoading ? (
              <div className="flex items-center justify-center w-full h-full text-center text-gray-500">
                Loading...
              </div>
            ) : error ? (
              <div className="flex items-center justify-center w-full h-full text-center text-red-500">
                Error: {error as string}
              </div>
            ) : (
              <Table results={data || { columns: [], data: [] }} />
            )}
          </div>
        </div>
      </main>
    </div>
  );
}

export default App;