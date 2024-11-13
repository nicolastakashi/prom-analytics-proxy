import { BookOpen } from "lucide-react";
import { sql } from '@codemirror/lang-sql';
import { Popover, PopoverContent, PopoverTrigger } from "../shadcn/popover";
import { Button } from "../shadcn/button";
import CodeMirror, { keymap } from '@uiw/react-codemirror';
import { tags } from '@lezer/highlight';
import { createTheme } from '@uiw/codemirror-themes';
import { useTheme } from "../../theme-provider";
import { QueryShortcut } from "../../fetch";
import { format } from 'sql-formatter';

export interface QueryProps {
    query: string;
    queryShortcuts: QueryShortcut[];
    handleQueryChange: (value: string) => void;
    handleShortcutClick: (query: string) => void;
    handleExecuteQuery: () => void;
    isLoading: boolean;
}

const darkPromqlHighlighter = createTheme({
    theme: 'dark',
    settings: {
        lineHighlight: '#1F2024',
        caret: '#fff',
    },
    styles: [
        { tag: tags.number, color: '#22c55e' },
        { tag: tags.string, color: '#fca5a5' },
        { tag: tags.keyword, color: '#14bfad' },
        { tag: tags.function(tags.variableName), color: '#14bfad' },
        { tag: tags.labelName, color: '#ff8585' },
        { tag: tags.operator },
        { tag: tags.modifier, color: '#14bfad' },
        { tag: tags.paren },
        { tag: tags.squareBracket },
        { tag: tags.brace },
        { tag: tags.invalid, color: '#ff3d3d' },
        { tag: tags.comment, color: '#9ca3af', fontStyle: 'italic' },
    ],
});

// react component using typescript
export default function Query(props: QueryProps) {
    const { theme } = useTheme()
    const keyMap = keymap.of([
        {
            key: 'Ctrl-Enter',
            run: () => {
                props.handleExecuteQuery();
                return true;
            },
        },
    ]);
    return (
        <div className="border-b bg-gray-200/50 dark:bg-gray-800/50 backdrop-blur supports-[backdrop-filter]:bg-gray-200/50 supports-[backdrop-filter]:dark:bg-gray-800/50">
            <div className="max-w-[85rem] mx-auto p-4">
                <div className="flex space-x-2">
                    <div className="flex-1 relative">
                        <div className="absolute left-3 top-3 text-muted-foreground">❯_</div>
                        <CodeMirror
                            value={props.query}
                            onChange={(newValue) => props.handleQueryChange(newValue)}
                            extensions={[sql({ upperCaseKeywords: true, schema: { queries: [] } }), keyMap]}
                            height="100%"
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
                            className="w-full pl-10 pr-4 py-2 bg-white dark:bg-background border border-input rounded-md focus:outline-none focus:ring-2 focus:ring-ring focus:border-input shadow-sm resize-none overflow-hidden min-h-[40px]"
                        />
                    </div>
                    <Popover>
                        <PopoverTrigger asChild>
                            <Button variant="outline" size="icon" className="shrink-0">
                                <BookOpen className="h-4 w-4" />
                                <span className="sr-only">Query shortcuts</span>
                            </Button>
                        </PopoverTrigger>
                        <PopoverContent className="w-80" align="end">
                            <div className="space-y-4">
                                <h4 className="font-medium leading-none">Query Shortcuts</h4>
                                <div className="space-y-2">
                                    {props.queryShortcuts.map((shortcut, index) => (
                                        <button
                                            key={index}
                                            onClick={() => props.handleShortcutClick(format(shortcut.query, { language: 'postgresql' }))}
                                            className="w-full text-left px-2 py-1 rounded-md hover:bg-accent hover:text-accent-foreground text-sm"
                                        >
                                            {shortcut.title}
                                        </button>
                                    ))}
                                </div>
                            </div>
                        </PopoverContent>
                    </Popover>
                    <Button variant="outline" onClick={props.handleExecuteQuery}>
                        {props.isLoading ? (
                            <svg
                                className="animate-spin h-6 w-7 text-white"
                                xmlns="http://www.w3.org/2000/svg"
                                fill="none"
                                viewBox="0 0 24 24"
                            >
                                <circle
                                    className="opacity-25"
                                    cx="12"
                                    cy="12"
                                    r="10"
                                    stroke="currentColor"
                                    strokeWidth="4"
                                ></circle>
                                <path
                                    className="opacity-75"
                                    fill="currentColor"
                                    d="M4 12a8 8 0 018-8V0C5.373 0 0 5.373 0 12h4zm2 5.291A7.962 7.962 0 014 12H0c0 3.042 1.135 5.824 3 7.938l3-2.647z"
                                ></path>
                            </svg>
                        ) : (
                            'Execute'
                        )}
                    </Button>
                </div>
            </div>
        </div>
    )
}