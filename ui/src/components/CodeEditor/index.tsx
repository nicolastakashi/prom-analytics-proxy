import React, { useState } from 'react';
import CodeMirror, { keymap } from '@uiw/react-codemirror';
import { sql, SQLNamespace } from '@codemirror/lang-sql';
import { FiCompass } from 'react-icons/fi';
import Modal from '../Modal'; // Importing the new Modal component
import QueryCard from '../QueryCard'; // Importing the new QueryCard component
import { useQuery } from 'react-query';
import fetch from '../../fetch';
import { toast } from 'react-toastify';
import './index.css';

interface CodeEditorProps {
    value: string;
    onChange: (value: string, callback?: () => void) => void;
    onSubmit: () => void;
    schema: SQLNamespace;
    isLoading: boolean; // New prop for loading state
}

interface QueryShortcut {
    title: string;
    description: string;
    query: string;
}

const CodeEditor: React.FC<CodeEditorProps> = (props) => {
    const [value, setValue] = useState(props.value);
    const [isModalOpen, setIsModalOpen] = useState(false); // State to manage modal visibility
    const { data, isLoading, refetch } = useQuery<QueryShortcut[]>(
        ['queryShortcuts'],
        () => fetch.QueryShortcuts(),
        {
            enabled: false, // Only run the query if the query is not empty,
            onError: (error) => {
                toast.error(`Error: ${(error as Error).message || 'An unknown error occurred'}`);
                closeModal(); // Close the modal on error
            },
        }
    );

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

    // Function to open the modal
    const openModal = () => {
        refetch(); // Fetch the query shortcuts
        setIsModalOpen(true);
    };

    // Function to close the modal
    const closeModal = () => {
        setIsModalOpen(false);
    };

    const onQueryShortcutsRun = (query: string) => {
        setValue(query);
        props.onChange(query);
        setTimeout(props.onSubmit, 600); // Run the query after 1 second
        closeModal(); // Close the modal after selecting a query
    }

    return (
        <div className="flex">
            <div className="flex-grow border border-[rgb(0,92,120)] rounded-md overflow-hidden flex relative">
                <div className="flex-grow overflow-hidden" style={{ borderRight: 'none' }}>
                    <CodeMirror
                        value={value}
                        onChange={(newValue) => handleChange(newValue)}
                        extensions={[sql({ upperCaseKeywords: true, schema: props.schema }), keyMap]}
                        height="100%"
                        className="w-full h-full rounded-md text-base font-mono"
                    />
                </div>

                {/* Button to open the modal */}
                <button
                    className="px-3 py-1 border border-[rgb(0,92,120)] rounded-l-md bg-blumine-800 text-white flex items-center justify-center"
                    onClick={openModal} // Open the modal on button click
                >
                    <FiCompass className="h-4 w-4" /> {/* Smaller icon size */}
                </button>

                {/* Run Button */}
                <button
                    className={`px-8 py-1 border h-full border-[rgb(0,92,120)] rounded-r-md self-start ${props.isLoading ? 'bg-blumine-900 cursor-not-allowed' : 'bg-blumine-900 text-white '} flex items-center justify-center`}
                    onClick={props.onSubmit}
                    disabled={props.isLoading}
                >
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
                        'Run'
                    )}
                </button>
            </div>

            {/* Modal Component */}
            <Modal isLoading={isLoading} isOpen={isModalOpen} onClose={closeModal} title="Common Queries">
                {data?.map((query) => (
                    <QueryCard
                        key={query.title}
                        onRun={onQueryShortcutsRun}
                        title={query.title}
                        description={query.description}
                        query={query.query}
                    />
                ))}
            </Modal>
        </div>
    );
};

export default CodeEditor;