import { useState } from 'react';
import Header from './components/Header';
import CodeEditor from './components/CodeEditor';
import Table from './components/Table';

interface Result {
  id: number;
  name: string;
  age: number;
}

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
  const [sqlQuery, setSqlQuery] = useState('');
  const [results, setResults] = useState<Array<Result>>(Array<Result>());

  const handleRunQuery = () => {
    setResults([
      { id: 1, name: 'Alice', age: 25 },
      { id: 2, name: 'Bob', age: 30 },
    ]);
  };

  return (
    <div className="min-h-screen flex flex-col">
      <Header />
      <main className="flex-grow p-4">
        <div className="flex flex-col h-full">
          <CodeEditor
            value={sqlQuery}
            onChange={setSqlQuery}
            onSubmit={handleRunQuery}
            schema={schema}
          />
          <Table results={results} />
        </div>
      </main>
    </div>
  );
}



export default App;