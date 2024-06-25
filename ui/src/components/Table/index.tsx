interface TableProps {
    results: Array<any>;
}

function Table(props: TableProps) {
    return (
        <div className="flex-grow overflow-auto">
            <table className="min-w-full bg-white">
                <thead>
                    <tr>
                        <th className="py-2 px-4 border-b">ID</th>
                        <th className="py-2 px-4 border-b">Name</th>
                        <th className="py-2 px-4 border-b">Age</th>
                    </tr>
                </thead>
                <tbody>
                    {props.results.map((result) => (
                        <tr key={result.id}>
                            <td className="py-2 px-4 border-b">{result.id}</td>
                            <td className="py-2 px-4 border-b">{result.name}</td>
                            <td className="py-2 px-4 border-b">{result.age}</td>
                        </tr>
                    ))}
                </tbody>
            </table>
        </div>
    )
}

export default Table;