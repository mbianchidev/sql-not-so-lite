import { useEffect, useRef, useState } from 'react';
import Editor, { loader } from '@monaco-editor/react';

declare global {
  var MonacoEnvironment: import('monaco-editor').Environment | undefined;
}

let engine: Promise<void> | null = null;

function prepareEditor() {
  engine ??= Promise.all([
    import('monaco-editor/editor/editor.api'),
    import('monaco-editor/languages/definitions/sql/register'),
    import('monaco-editor/editor/editor.worker?worker'),
  ]).then(([monaco, , { default: EditorWorker }]) => {
    globalThis.MonacoEnvironment = { getWorker: () => new EditorWorker() };
    monaco.editor.defineTheme('sqnsl-dark', {
      base: 'vs-dark',
      inherit: true,
      rules: [
        { token: 'keyword', foreground: 'A6D5BC' },
        { token: 'comment', foreground: '98A2AD' },
        { token: 'string', foreground: 'E1BC83' },
        { token: 'number', foreground: 'B7BFE4' },
      ],
      colors: {
        'editor.background': '#151719',
        'editor.foreground': '#EFF2F4',
        'editorLineNumber.foreground': '#98A2AD',
        'editor.lineHighlightBackground': '#1B1D20',
        'editor.selectionBackground': '#334C40',
        'editorCursor.foreground': '#A6D5BC',
      },
    });
    monaco.editor.defineTheme('sqnsl-light', {
      base: 'vs',
      inherit: true,
      rules: [
        { token: 'keyword', foreground: '296B4C' },
        { token: 'comment', foreground: '5B6872' },
      ],
      colors: { 'editor.background': '#FFFFFF', 'editor.foreground': '#192127' },
    });
    loader.config({ monaco });
  }).catch((err: unknown) => {
    engine = null;
    throw err;
  });
  return engine;
}

interface Props {
  value: string;
  onChange: (value: string) => void;
  onExecute: () => void;
  darkMode: boolean;
  height?: string;
}

export function CodeEditor({ value, onChange, onExecute, darkMode, height = 'var(--sql-editor-height)' }: Props) {
  const [ready, setReady] = useState(false);
  const [error, setError] = useState<string | null>(null);
  const [attempt, setAttempt] = useState(0);
  const execute = useRef(onExecute);

  useEffect(() => { execute.current = onExecute; }, [onExecute]);
  useEffect(() => {
    let active = true;
    void prepareEditor().then(() => {
      if (active) setReady(true);
    }).catch((err: unknown) => {
      console.error('Unable to load SQL editor', err);
      if (active) setError(err instanceof Error ? err.message : 'SQL editor failed to load.');
    });
    return () => { active = false; };
  }, [attempt]);

  if (error) return (
    <div className="error-msg" role="alert">
      {error}
      <button className="btn-sm" onClick={() => { setError(null); setAttempt((current) => current + 1); }}>Retry editor</button>
    </div>
  );

  return (
    <div className="editor-container" style={{ minHeight: height }}>
      {ready ? (
        <Editor
          height={height}
          language="sql"
          value={value}
          onChange={(next) => onChange(next ?? '')}
          theme={darkMode ? 'sqnsl-dark' : 'sqnsl-light'}
          loading={<div className="loading" role="status">Opening SQL editor...</div>}
          options={{
            ariaLabel: 'SQL statement',
            minimap: { enabled: false },
            fontSize: 13,
            fontFamily: 'SFMono-Regular, Consolas, Liberation Mono, monospace',
            lineHeight: 24,
            lineNumbers: 'on',
            padding: { top: 20, bottom: 20 },
            scrollBeyondLastLine: false,
            wordWrap: 'on',
            automaticLayout: true,
            tabSize: 2,
          }}
          onMount={(editor, monaco) => {
            editor.addCommand(monaco.KeyMod.CtrlCmd | monaco.KeyCode.Enter, () => execute.current());
          }}
        />
      ) : <div className="loading" role="status">Loading SQL editor...</div>}
    </div>
  );
}
