import React from 'react';
import { format } from 'date-fns';
import {
    useReactTable,
    ColumnDef,
    getCoreRowModel,
    flexRender,
} from '@tanstack/react-table';
import { useVirtualizer } from '@tanstack/react-virtual';
import formatDuration from 'format-duration';

export interface Result {
    columns: string[];
    data: Array<any>;
}

interface TableProps {
    results: Result;
    isLoading: boolean;
}

const dateFormat = (date: string): string => {
    if (date == "0001-01-01T00:00:00Z") {
        return "N/A"
    }

    const dateObj = new Date(date);
    return format(dateObj, 'yyyy-MM-dd HH:mm:ss');
}

const durationFormat = (duration: number): string => {
    return formatDuration(duration, {
        ms: true
    })
}
const typeFormat = (type: string): string => {
    return type.replace(/\w+/g, (w) => w[0].toUpperCase() + w.slice(1).toLowerCase());;
}
// create a map of string:function
const cellFormatMap: { [key: string]: (value: any) => string } = {
    'ts': dateFormat,
    'timeparam': dateFormat,
    'duration': durationFormat,
    'start': dateFormat,
    'end': dateFormat,
    'type': typeFormat,
}


function Table({ results, isLoading }: TableProps) {
    const parentRef = React.useRef<HTMLDivElement>(null);

    const getColumnWidth = (rows: any[], accessor: string, headerText: string | any[]) => {
        const maxWidth = 350
        const magicSpacing = 15
        const cellLength = Math.max(
            ...rows.map(row => (`${row[accessor]}` || '').length),
            headerText.length,
        )
        if (headerText === "TS") {
            console.log(rows.map(row => (`${row[accessor]}` || '').length))
        }
        return Math.min(maxWidth, cellLength * magicSpacing)
    }

    // Define columns for the table
    const columns = React.useMemo<ColumnDef<any>[]>(
        () =>
            results?.columns.map((column) => ({
                accessorKey: column,
                header: column.toUpperCase(),
                width: getColumnWidth(results.data, column, column.toUpperCase()),
                size: getColumnWidth(results.data, column, column.toUpperCase()),
                cell: (info) => {
                    const value = info.getValue();
                    const formatFunction = cellFormatMap[column];
                    return formatFunction ? formatFunction(value) : String(value);
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
        overscan: 5, // Render extra rows outside the visible area for smoother scrolling
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
                            <table className="bg-white" style={{ tableLayout: 'fixed', width: '100%' }}>
                                <thead className="bg-blumine-800 text-white">
                                    {table.getHeaderGroups().map((headerGroup) => (
                                        <tr key={headerGroup.id}>
                                            {headerGroup.headers.map((header) => (
                                                <th
                                                    style={{
                                                        width: header.getSize(),
                                                    }}
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
                                                        style={{
                                                            width: cell.column.getSize()
                                                        }}
                                                        key={cell.id}
                                                        className="py-3 px-4 text-left">
                                                        {flexRender(cell.column.columnDef.cell, cell.getContext())}
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