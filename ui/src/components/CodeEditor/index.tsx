import CodeMirror, { keymap, } from '@uiw/react-codemirror';
import { SQLNamespace, sql } from '@codemirror/lang-sql';
import { vscodeLight } from '@uiw/codemirror-theme-vscode';

interface CodeEditorProps {
    value: string;
    onChange: (value: string) => void;
    onSubmit: () => void;
    schema: SQLNamespace;

}

function CodeEditor(props: CodeEditorProps) {
    const keyMap = keymap.of([
        {
            key: 'Ctrl-Enter',
            run: (_) => {
                props.onSubmit();
                return true;
            },
        },
    ])
    return (
        <div className="flex mb-4">
            <div className="flex-grow border border-gray-300 rounded-md overflow-hidden">
                <CodeMirror
                    value={props.value}
                    onChange={props.onChange}
                    lang='sql'
                    className='w-full h-full'
                    theme={vscodeLight}
                    height='100%'
                    extensions={[sql({ upperCaseKeywords: true, schema: props.schema }), keyMap]}
                />
            </div>
            <button
                className="ml-4 bg-blue-500 text-white px-8 py-2 rounded-md self-start"
                onClick={props.onSubmit}>
                Run
            </button>
        </div>
    )
}

export default CodeEditor;