import { useQuery } from '@tanstack/react-query';
import { getConfigurations } from '@/api/queries';
import { Card, CardContent, CardHeader, CardTitle } from '@/components/ui/card';
import { Prism as SyntaxHighlighter } from 'react-syntax-highlighter';
import {
  vscDarkPlus,
  oneLight,
} from 'react-syntax-highlighter/dist/esm/styles/prism';
import { useTheme } from 'next-themes';

export default function Settings() {
  const { theme } = useTheme();
  const isDarkTheme = theme === 'dark';

  const { data: config, isLoading } = useQuery({
    queryKey: ['configurations'],
    queryFn: () => getConfigurations('yaml'),
    refetchOnMount: false,
  });

  const loadingContent = (
    <Card className="w-full max-w-3xl">
      <CardHeader>
        <CardTitle>Backend Configuration</CardTitle>
      </CardHeader>
      <CardContent>
        <p>Loading configuration...</p>
      </CardContent>
    </Card>
  );

  const loadedContent = (
    <Card className="w-full max-w-3xl">
      <CardHeader>
        <CardTitle>Backend Configuration</CardTitle>
      </CardHeader>
      <CardContent>
        <SyntaxHighlighter
          language="yaml"
          style={isDarkTheme ? vscDarkPlus : oneLight}
          customStyle={{
            margin: 0,
            fontSize: '0.875rem',
          }}
          showLineNumbers={false}
          children={String(config || '')}
          wrapLongLines
          codeTagProps={{
            style: {
              fontFamily: 'var(--font-mono)',
            },
          }}
        />
      </CardContent>
    </Card>
  );

  return (
    <div className="flex flex-col flex-1 space-y-4 p-4 md:p-8 pt-6">
      <div className="flex items-center justify-between">
        <h2 className="text-3xl font-bold tracking-tight">Settings</h2>
      </div>
      <div className="flex flex-grow items-center">
        {isLoading ? loadingContent : loadedContent}
      </div>
    </div>
  );
}
