import React from 'react';
import { format, parseISO } from 'date-fns';
import { toZonedTime } from 'date-fns-tz';

export interface Result {
    columns: string[],
    data: Array<any>
}

interface TableProps {
    results: Result;
}

function Table({ results }: TableProps) {
    const formatDateToUTC = (dateString: string) => {
        const date = parseISO(dateString);
        const zonedDate = toZonedTime(date, 'UTC');
        return format(zonedDate, 'yyyy-MM-dd HH:mm:ss \'UTC\'');
    };

    return (
        <div className="bg-white shadow-md rounded-md overflow-hidden h-full">
            {results?.columns?.length > 0 ? (
                <div className="overflow-x-auto border border-gray-300 rounded-md h-full flex flex-col">
                    <table className="min-w-full bg-white">
                        <thead className="bg-blumine-800 text-white">
                            <tr>
                                {results.columns.map((column) => (
                                    <th key={column} className="w-1/3 text-left py-3 px-4 font-semibold text-sm">
                                        {column}
                                    </th>
                                ))}
                            </tr>
                        </thead>
                        <tbody className="text-gray-700">
                            {results.data.map((row, rowIndex) => (
                                <tr key={rowIndex} className="bg-gray-100 border-b border-gray-200">
                                    {results.columns.map((column) => (
                                        <td key={column} className="w-1/3 text-left py-3 px-4">
                                            {column === 'TS' || column === 'TimeParam' ? (
                                                formatDateToUTC(row[column])
                                            ) : (
                                                row[column]
                                            )}
                                        </td>
                                    ))}
                                </tr>
                            ))}
                        </tbody>
                    </table>
                </div>
            ) : (
                <div className="flex items-center justify-center h-full text-center text-gray-500" style={{ minHeight: '150px' }}>
                    No results found
                </div>
            )}
        </div>
    );
}

export default Table;