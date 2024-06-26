import { useState } from 'react';
import Header from './components/Header';
import CodeEditor from './components/CodeEditor';
import Table, { Result } from './components/Table';
import { useQuery } from 'react-query';
import fetchAnalyticsData from './fetch';


const schema = {
  queries: [
    'ts',
    'fingerprint',
    'query_param',
    'time_param',
    'label_matchers_list',
    'duration_ms',
    'status_code',
    'body_size_bytes'
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
    refetch()
  };

  if (isLoading) {
    return <div>Loading...</div>;
  }

  if (error) {
    return <div>Error: {error as string}</div>;
  }

  console.log(data)

  return (
    <div className="min-h-screen flex flex-col">
      <Header />
      <main className="flex-grow p-4">
        <div className="flex flex-col h-full">
          <CodeEditor
            value={query}
            onChange={setQuery}
            onSubmit={handleRunQuery}
            schema={schema}
          />
          <Table results={data || { columns: [], data: [] }} />
        </div>
      </main>
    </div>
  );
}



export default App;