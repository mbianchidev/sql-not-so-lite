import { useEffect, useRef, useState } from 'react';
import { CRUD_METHODS, hasAdminToken, sendAPIRequest, type APIExposure, type APIRequestResponse, type CRUDMethod } from '../api/client';
import { Icon } from './Icon';

interface Props {
  exposure: APIExposure;
  initialToken: string;
  onClose: () => void;
}

export function APIRequestEditor({ exposure, initialToken, onClose }: Props) {
  const [method, setMethod] = useState<CRUDMethod>('GET');
  const [url, setUrl] = useState(`/api/data/${encodeURIComponent(exposure.alias)}/${encodeURIComponent(exposure.tables[0])}?limit=50`);
  const [headers, setHeaders] = useState('{\n  "Accept": "application/json"\n}');
  const [body, setBody] = useState('{}');
  const [token, setToken] = useState(initialToken);
  const [authentication, setAuthentication] = useState<'token' | 'admin'>('token');
  const [response, setResponse] = useState<APIRequestResponse | null>(null);
  const [error, setError] = useState<string | null>(null);
  const [message, setMessage] = useState<string | null>(null);
  const [sending, setSending] = useState(false);
  const controller = useRef<AbortController | null>(null);
  const mounted = useRef(true);
  const hasBody = method === 'POST' || method === 'PATCH';

  useEffect(() => { if (initialToken) setToken(initialToken); }, [initialToken]);
  useEffect(() => {
    mounted.current = true;
    return () => { mounted.current = false; controller.current?.abort(); };
  }, []);

  const send = async () => {
    if (controller.current) return;
    const current = new AbortController();
    controller.current = current;
    setSending(true); setError(null); setMessage(null); setResponse(null);
    try {
      const result = await sendAPIRequest({ method, url, headers, body, token, useAdminSession: authentication === 'admin', signal: current.signal });
      if (mounted.current) setResponse(result);
    } catch (err) {
      if (mounted.current) {
        if (current.signal.aborted) setMessage('Request cancelled. A mutation may already have been applied by the server.');
        else if (err instanceof DOMException && err.name === 'TimeoutError') setError('Request timed out after 30 seconds. The server may already have applied a mutation.');
        else setError(err instanceof TypeError ? `Request failed: ${err.message}. Check the URL and daemon connection.` : err instanceof Error ? err.message : 'Request failed.');
      }
    } finally {
      controller.current = null;
      if (mounted.current) setSending(false);
    }
  };

  return (
    <section className="api-request-editor" aria-label="API request editor">
      <div className="section-heading">
        <div><h3><Icon name="terminal" /> API request editor</h3><p>Send requests to <strong>{exposure.alias}</strong> and inspect the actual HTTP response.</p></div>
        <button className="btn-icon" onClick={onClose} aria-label="Close request editor"><Icon name="close" /></button>
      </div>
      <form onSubmit={(event) => { event.preventDefault(); void send(); }}>
        <div className="request-target">
          <select value={method} aria-label="Request method" disabled={sending} onChange={(event) => {
            const next = CRUD_METHODS.find((value) => value === event.target.value);
            if (next) setMethod(next);
          }}>{CRUD_METHODS.map((value) => <option key={value}>{value}</option>)}</select>
          <input value={url} onChange={(event) => setUrl(event.target.value)} aria-label="Request URL" disabled={sending} required />
          <button className="btn-primary" type="submit" disabled={sending} aria-label="Send request"><Icon name="play" />{sending ? 'Sending...' : 'Send'}</button>
          {sending && <button className="btn-sm" type="button" onClick={() => controller.current?.abort()} aria-label="Cancel request">Cancel</button>}
        </div>
        <div className="request-authentication">
          <label className="api-field">Authentication
            <select value={authentication} aria-label="Request authentication" disabled={sending} onChange={(event) => setAuthentication(event.target.value === 'admin' ? 'admin' : 'token')}>
              <option value="token">API token</option>
              {hasAdminToken() && <option value="admin">Admin session</option>}
            </select>
          </label>
          <label className="api-field">Request API token
            <input type="password" value={token} onChange={(event) => setToken(event.target.value)} autoComplete="off" aria-label="Request API token" placeholder="Paste a scoped API token" disabled={sending || authentication === 'admin'} />
          </label>
        </div>
        <details className="request-header-details">
          <summary>Edit request headers</summary>
          <label className="api-field">Headers (JSON object)
            <textarea value={headers} onChange={(event) => setHeaders(event.target.value)} aria-label="Request headers" rows={4} disabled={sending} spellCheck={false} />
          </label>
        </details>
        {hasBody && <label className="api-field">JSON body
            <textarea value={body} onChange={(event) => setBody(event.target.value)} aria-label="Request body" rows={4} disabled={sending || !hasBody} spellCheck={false} />
          </label>}
      </form>
      {!exposure.writable && method !== 'GET' && <p className="api-warning">This API is read-only. Write requests will be rejected by the server.</p>}
      <p className="api-warning">Same-origin CRUD APIs only. Tokens stay in memory. Cancellation stops waiting; it cannot undo a mutation.</p>
      {error && <div className="error-msg" role="alert">{error}</div>}
      {message && <div className="scan-result" role="status">{message}</div>}
      {response ? (
        <div className="api-response">
          <div className="response-summary">
            <span className={response.status >= 400 ? 'response-status error' : 'response-status'} role="status" aria-label="Response status">{response.status} {response.statusText}</span>
            <span>{Math.round(response.durationMs)} ms</span>
          </div>
          {response.truncated && <div className="error-msg" role="alert">Response body truncated at 1 MiB. Use an external client to retrieve the complete response.</div>}
          <label className="api-field">Response body
            <textarea value={response.body} readOnly aria-label="Response body" rows={7} spellCheck={false} />
          </label>
          {!response.body && <p className="api-warning">No response body</p>}
          <details className="response-header-details"><summary>Response headers</summary><label className="api-field">
            <textarea value={JSON.stringify(response.headers, null, 2)} readOnly aria-label="Response headers" rows={4} spellCheck={false} />
          </label></details>
        </div>
      ) : !sending && <p className="api-warning">Send a request to see its status, headers and raw body here. JSON numbers are displayed without rounding.</p>}
    </section>
  );
}
