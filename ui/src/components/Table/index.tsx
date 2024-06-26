export interface Result {
    columns: string[],
    data: Array<any>
}
interface TableProps {
    results: Result;
}

function Table({ results }: TableProps) {
    debugger;
    if (results?.columns?.length === 0) {
        return null;
    }
    return (
        <div className="overflow-x-auto">
            <table className="min-w-full bg-white">
                <thead className="bg-gray-800 text-white">
                    <tr>
                        {results.columns.map((column) => (
                            <th key={column} className="w-1/3 text-left py-3 px-4 uppercase font-semibold text-sm">
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
                                    {row[column]}
                                </td>
                            ))}
                        </tr>
                    ))}
                </tbody>
            </table>
        </div>
        // <div className="flex-grow overflow-auto">
        //     <table className="min-w-full bg-white">
        //         <thead>
        //             <tr>
        //                 <th className="py-2 px-4 border-b">ID</th>
        //                 <th className="py-2 px-4 border-b">Name</th>
        //                 <th className="py-2 px-4 border-b">Age</th>
        //             </tr>
        //         </thead>
        //         <tbody>
        //             {/* {props.results.map((result) => (
        //                 <tr key={result.id}>
        //                     <td className="py-2 px-4 border-b">{result.id}</td>
        //                     <td className="py-2 px-4 border-b">{result.name}</td>
        //                     <td className="py-2 px-4 border-b">{result.age}</td>
        //                 </tr>
        //             ))} */}
        //         </tbody>
        //     </table>
        // </div>
    )
}

export default Table;