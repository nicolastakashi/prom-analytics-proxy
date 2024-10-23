import React from 'react';
import { format } from 'date-fns';
import {
    useReactTable,
    ColumnDef,
    getCoreRowModel,
    flexRender,
} from '@tanstack/react-table';
import { useVirtualizer } from '@tanstack/react-virtual';

export interface Result {
    columns: string[];
    data: Array<any>;
}

interface TableProps {
    results: Result;
    isLoading: boolean;
}

function Table({ results, isLoading }: TableProps) {
    const parentRef = React.useRef<HTMLDivElement>(null);

    // Define columns for the table
    const columns = React.useMemo<ColumnDef<any>[]>(
        () =>
            results?.columns.map((column) => ({
                accessorKey: column,
                header: column.toUpperCase(),
                cell: (info) => {
                    console.log(column)
                    const value = info.getValue();
                    return column === 'ts' || column === 'timeparam' ? format(new Date(value as string), 'yyyy-MM-dd HH:mm:ss') : value;
                },
            })),
        [results]
    );

    // Initialize the Tanstack table instance using useReactTable
    const table = useReactTable({
        data: results?.data || [],
        columns,
        getCoreRowModel: getCoreRowModel(),
    });

    // Set up the virtualizer
    const rowVirtualizer = useVirtualizer({
        count: table.getRowModel().rows.length,
        getScrollElement: () => parentRef.current,
        estimateSize: () => 50, // Approximate row height
        overscan: 10, // Render extra rows outside the visible area for smoother scrolling
    });

    return (
        <div className="bg-white shadow-md overflow-hidden h-full border border-[rgb(0,92,120)] rounded-md">
            {isLoading ? (
                <div className="flex items-center justify-center h-full min-h-[150px]">
                    {/* Loading spinner */}
                    <div className="spinner-border animate-spin inline-block w-8 h-8 border-4 rounded-full" role="status">
                        <span className="sr-only">Loading...</span>
                    </div>
                </div>
            ) : results?.columns?.length > 0 ? (
                <>
                    {/* Table */}
                    <div className="overflow-x-auto border border-gray-300 rounded-md h-full flex flex-col">
                        <div ref={parentRef} className="overflow-y-auto max-h-[600px]">
                            <table className="min-w-full bg-white">
                                <thead className="bg-blumine-800 text-white">
                                    {table.getHeaderGroups().map((headerGroup) => (
                                        <tr key={headerGroup.id}>
                                            {headerGroup.headers.map((header) => (
                                                <th
                                                    key={header.id}
                                                    className="text-left py-3 px-4 font-semibold text-sm"
                                                >
                                                    {flexRender(header.column.columnDef.header, header.getContext())}
                                                </th>
                                            ))}
                                        </tr>
                                    ))}
                                </thead>
                                <tbody
                                    className="text-gray-700"
                                    style={{
                                        height: `${rowVirtualizer.getTotalSize()}px`,
                                        position: 'relative'
                                    }}
                                >
                                    {rowVirtualizer.getVirtualItems().map((virtualRow) => {
                                        const row = table.getRowModel().rows[virtualRow.index];
                                        return (
                                            <tr
                                                key={row.id}
                                                className="bg-gray-100 border-b border-gray-200"
                                            >
                                                {row.getVisibleCells().map((cell) => (
                                                    <td
                                                        key={cell.id}
                                                        className="py-3 px-4 text-left">
                                                        <span>{flexRender(cell.column.columnDef.cell, cell.getContext())}</span>
                                                    </td>
                                                ))}
                                            </tr>
                                        );
                                    })}
                                </tbody>
                            </table>
                        </div>
                    </div>
                </>
            ) : (
                <div className="flex items-center justify-center h-full text-center text-gray-500" style={{ minHeight: '150px' }}>
                    No data available
                </div>
            )}
        </div>
    );
}

export default Table;