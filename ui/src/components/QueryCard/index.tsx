import React from 'react';
import CodeMirror from '@uiw/react-codemirror';
import { sql } from '@codemirror/lang-sql';
import { format } from 'sql-formatter';

interface QueryCardProps {
    title: string;
    description: string;
    query: string;
    onRun: (query: string) => void; // Add a callback function for the Run button
}

const QueryCard: React.FC<QueryCardProps> = ({ title, description, query, onRun }) => {
    return (
        <div className="border p-4 rounded-lg mb-4">
            {/* Flex container to align title and Run button */}
            <div className="flex justify-between items-center mb-2">
                <h3 className="text-lg font-semibold">{title}</h3>
            </div>
            <p className="text-sm mb-3">{description}</p>
            <div className="flex-grow border border-[rgb(0,92,120)] rounded-md overflow-hidden flex relative">
                <div className="flex-grow overflow-hidden" style={{ borderRight: 'none' }}>
                    <CodeMirror
                        readOnly={true}
                        value={format(query, { language: 'postgresql' })}
                        extensions={[sql()]}
                        className="w-full h-full rounded-md text-base font-mono border"
                    />
                </div>
                <button
                    className={`px-8 py-1 border h-full border-[rgb(0,92,120)] rounded-r-md self-start bg-blumine-900 text-white flex items-center justify-center`}
                    onClick={() => onRun(query)}
                >
                    Run
                </button>
            </div>
        </div>
    );
};

export default QueryCard;