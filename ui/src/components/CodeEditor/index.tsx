import React, { useState } from 'react';
import CodeMirror, { keymap } from '@uiw/react-codemirror';
import { sql, SQLNamespace } from '@codemirror/lang-sql';
import { vscodeLight } from '@uiw/codemirror-theme-vscode';

interface CodeEditorProps {
    value: string;
    onChange: (value: string) => void;
    onSubmit: () => void;
    schema: SQLNamespace;
    isLoading: boolean; // New prop for loading state
}

const CodeEditor: React.FC<CodeEditorProps> = (props) => {
    const [value, setValue] = useState(props.value);

    const handleChange = (newValue: string) => {
        setValue(newValue);
        props.onChange(newValue);
    };

    const keyMap = keymap.of([
        {
            key: 'Ctrl-Enter',
            run: () => {
                props.onSubmit();
                return true;
            },
        },
    ]);

    return (
        <div className="flex mb-4">
            <div className="flex-grow border border-gray-300 rounded-md overflow-hidden flex">
                <div className="flex-grow overflow-hidden" style={{ borderRight: 'none' }}>
                    <CodeMirror
                        value={value}
                        onChange={(newValue) => handleChange(newValue)}
                        extensions={[sql({ upperCaseKeywords: true, schema: props.schema }), keyMap]}
                        theme={vscodeLight}
                        height="100%"
                        className="w-full h-full"
                        style={{ borderRadius: '0.375rem 0 0 0.375rem' }} // Adjusted border-radius
                    />
                </div>
                <button
                    className={`px-8 py-2 rounded-md self-start ${props.isLoading ? 'bg-blumine-700 cursor-not-allowed' : 'bg-blumine-700 text-white'} flex items-center justify-center`}
                    onClick={props.onSubmit}
                    style={{ borderRadius: '0 0.375rem 0.375rem 0', marginLeft: 0 }} // Adjusted border-radius
                    disabled={props.isLoading}
                >
                    {props.isLoading ? (
                        <svg
                            className="animate-spin h-5 w-5 text-white"
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
                        'Run'
                    )}
                </button>
            </div>
        </div>
    );
};

export default CodeEditor;