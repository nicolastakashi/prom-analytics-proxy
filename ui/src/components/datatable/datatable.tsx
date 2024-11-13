import { format } from 'date-fns';
import formatDuration from 'format-duration';
import {
    Table,
    TableBody,
    TableCell,
    TableHead,
    TableHeader,
    TableRow,
} from "../shadcn/table";
import { QueryResult } from "../../fetch";
import { useMemo, useRef } from 'react';
import { useReactTable, ColumnDef, getCoreRowModel, flexRender } from '@tanstack/react-table';
import { useVirtualizer } from '@tanstack/react-virtual';

export interface DataTableProps {
    result: QueryResult;
}

const dateFormat = (date: string): string => {
    if (date == "0001-01-01T00:00:00Z") {
        return "N/A";
    }

    const dateObj = new Date(date);
    return format(dateObj, 'yyyy-MM-dd HH:mm:ss');
}

const durationFormat = (duration: number): string => {
    return formatDuration(duration, { ms: true });
}

const typeFormat = (type: string): string => {
    return type.replace(/\w+/g, (w) => w[0].toUpperCase() + w.slice(1).toLowerCase());
}

const statusCodeFormat = (statuscode: number) => {
    return <div
        className={`inline-flex items-center rounded-full px-2.5 py-0.5 text-xs font-medium
        ${statuscode >= 200 && statuscode < 300
                ? 'bg-green-100 text-green-800 dark:bg-green-900 dark:text-green-300'
                : statuscode >= 400 && statuscode < 500
                    ? 'bg-yellow-100 text-yellow-800 dark:bg-yellow-900 dark:text-yellow-300'
                    : statuscode >= 500
                        ? 'bg-red-100 text-red-800 dark:bg-red-900 dark:text-red-300'
                        : 'bg-gray-100 text-gray-800 dark:bg-gray-900 dark:text-gray-300'
            }`}
    >
        {statuscode}
    </div>
}

const cellFormatMap: { [key: string]: (value: any) => string | JSX.Element } = {
    'ts': dateFormat,
    'timeParam': dateFormat,
    'duration': durationFormat,
    'start': dateFormat,
    'end': dateFormat,
    'type': typeFormat,
    'statuscode': statusCodeFormat,
}

export default function DataTable({ result }: DataTableProps) {
    const parentRef = useRef<HTMLDivElement>(null);

    const getColumnWidth = (rows: any[], accessor: string, headerText: string | any[]) => {
        const maxWidth = 350
        const magicSpacing = 10
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
    const columns = useMemo<ColumnDef<any>[]>(
        () =>
            result?.columns.map((column) => ({
                accessorKey: column,
                header: column.toUpperCase(),
                width: getColumnWidth(result.data, column, column.toUpperCase()),
                size: getColumnWidth(result.data, column, column.toUpperCase()),
                cell: (info) => {
                    const value = info.getValue();
                    const formatFunction = cellFormatMap[column];
                    if (formatFunction) {
                        return formatFunction(value);
                    }

                    // if column contains duration, format it
                    if (column.includes('duration')) {
                        return durationFormat(Number(value));
                    }

                    return value;
                },
            })),
        [result]
    );


    const table = useReactTable({
        data: result?.data || [],
        columns,
        getCoreRowModel: getCoreRowModel(),

    });

    const rowVirtualizer = useVirtualizer({
        count: table.getRowModel().rows.length,
        getScrollElement: () => parentRef.current,
        estimateSize: () => 50, // Approximate row height
        overscan: 5, // Render extra rows outside the visible area for smoother scrolling
    });


    return (
        <div className="flex-1 overflow-auto" ref={parentRef}>
            <div className="max-w-[85rem] mx-auto p-4">
                {result.data.length > 0 ? (
                    <div className="space-y-4">
                        <div className="rounded-md border bg-background">
                            <Table style={{ tableLayout: 'fixed', width: '100%' }}>
                                <TableHeader>
                                    <TableRow>
                                        {table.getHeaderGroups().map(headerGroup => (
                                            headerGroup.headers.map(header => (
                                                <TableHead style={{
                                                    width: header.getSize(),
                                                }} key={header.id}>
                                                    {flexRender(header.column.columnDef.header, header.getContext())}
                                                </TableHead>
                                            ))
                                        ))}
                                        <TableHead className="w-[20px]"></TableHead>
                                    </TableRow>
                                </TableHeader>
                                <TableBody style={{
                                    height: `${rowVirtualizer.getTotalSize()}px`,
                                    position: 'relative'
                                }}>
                                    {rowVirtualizer.getVirtualItems().map(virtualRow => {
                                        const rowIndex = virtualRow.index;
                                        const row = table.getRowModel().rows[rowIndex];
                                        return (
                                            <TableRow key={row.id}>
                                                {
                                                    row.getVisibleCells().map(cell => {
                                                        if (cell.column.id === 'statusCode') {
                                                            let value = cell.getValue() as number;
                                                            return (
                                                                <TableCell key={cell.id}>
                                                                    <div
                                                                        className={`inline-flex items-center rounded-full px-2.5 py-0.5 text-xs font-medium
                                                                            ${value >= 200 && value < 300
                                                                                ? 'bg-green-100 text-green-800 dark:bg-green-900 dark:text-green-300'
                                                                                : value >= 400 && value < 500
                                                                                    ? 'bg-yellow-100 text-yellow-800 dark:bg-yellow-900 dark:text-yellow-300'
                                                                                    : value >= 500
                                                                                        ? 'bg-red-100 text-red-800 dark:bg-red-900 dark:text-red-300'
                                                                                        : 'bg-gray-100 text-gray-800 dark:bg-gray-900 dark:text-gray-300'
                                                                            }`}
                                                                    >
                                                                        {flexRender(cell.column.columnDef.cell, cell.getContext())}
                                                                    </div>
                                                                </TableCell>
                                                            );
                                                        }
                                                        return (
                                                            <TableCell style={{
                                                                width: cell.column.getSize()
                                                            }} key={cell.id}>
                                                                {flexRender(cell.column.columnDef.cell, cell.getContext())}
                                                            </TableCell>
                                                        )
                                                    })
                                                }
                                            </TableRow>
                                        );
                                    })}
                                </TableBody>
                            </Table>
                        </div>
                    </div>
                ) : (
                    <div className="flex items-center justify-center h-[calc(100vh-12rem)] text-muted-foreground">
                        No results to display. Try running a query.
                    </div>
                )}
            </div>
        </div>
    );
}