import { useCallback, useEffect, useState } from 'react';
import { api, type APIExposure, type APISource, type TableInfo } from '../api/client';
import { Icon } from './Icon';
import { APIRequestEditor } from './APIRequestEditor';

interface Props {
  source: APISource;
  tables: TableInfo[];
  onClose: () => void;
}

export function APIExposurePanel({ source, tables, onClose }: Props) {
  const managed = source.managed_database;
  const discovered = source.discovered_id;
  const [alias, setAlias] = useState('');
  const [selected, setSelected] = useState<string[]>([]);
  const [writable, setWritable] = useState(false);
  const [exposures, setExposures] = useState<APIExposure[]>([]);
  const [credential, setCredential] = useState<{ exposure: APIExposure; token: string } | null>(null);
  const [error, setError] = useState<string | null>(null);
  const [message, setMessage] = useState<string | null>(null);
  const [busy, setBusy] = useState(false);
  const [testing, setTesting] = useState<APIExposure | null>(null);

  const refresh = useCallback(async () => {
    const all = await api.listExposures();
    setExposures(all.filter((entry) => managed
      ? entry.managed_database === managed
      : !entry.managed_database && entry.database_id === discovered));
  }, [managed, discovered]);
  useEffect(() => {
    void refresh().catch((err: unknown) => setError(err instanceof Error ? err.message : 'Unable to load APIs.'));
  }, [refresh]);

  const perform = async (action: () => Promise<void>) => {
    setBusy(true); setError(null); setMessage(null);
    try { await action(); }
    catch (err) { setError(err instanceof Error ? err.message : 'API operation failed.'); }
    finally { setBusy(false); }
  };
  const create = () => perform(async () => {
    const result = await api.createExposure(source, alias.trim(), selected, writable);
    setCredential(result);
    setMessage('API created. Copy this token now; it will not be shown again.');
    await refresh();
  });
  const revoke = (entry: APIExposure) => {
    if (!confirm(`Revoke API "${entry.alias}"? Clients using its token will lose access. Database files are not deleted.`)) return;
    void perform(async () => {
      await api.revokeExposure(entry.alias);
      if (credential?.exposure.alias === entry.alias) setCredential(null);
      if (testing?.alias === entry.alias) setTesting(null);
      await refresh();
      setMessage(`API "${entry.alias}" revoked.`);
    });
  };
  const copy = async (value: string) => {
    setError(null);
    try {
      if (!navigator.clipboard) throw new Error('Clipboard access requires HTTPS or localhost. Select and copy the text manually.');
      await navigator.clipboard.writeText(value);
      setMessage('Copied to clipboard.');
    } catch (err) { setError(err instanceof Error ? err.message : 'Copy failed. Select the text and copy it manually.'); }
  };
  const endpoint = credential ? new URL(`/api/data/${credential.exposure.alias}/${encodeURIComponent(credential.exposure.tables[0])}`, window.location.origin).href : '';
  const example = credential ? `curl -H 'Authorization: Bearer ${credential.token}' '${endpoint}?limit=50'` : '';

  if (testing) return <APIRequestEditor exposure={testing} initialToken={credential?.exposure.alias === testing.alias ? credential.token : ''} onClose={() => setTesting(null)} />;

  return (
    <section className="api-exposure-panel" aria-label="API exposure settings">
      <div className="section-heading">
        <div><h3><Icon name="terminal" /> Expose REST API</h3><p>Create a scoped URL and token for this database.</p></div>
        <button className="btn-icon" onClick={onClose} aria-label="Close API settings"><Icon name="close" /></button>
      </div>
      {error && <div className="error-msg" role="alert">{error}</div>}
      {message && <div className="scan-result" role="status">{message}</div>}
      <form className="api-exposure-form" onSubmit={(event) => { event.preventDefault(); void create(); }}>
        <label className="api-field">API alias
          <input value={alias} onChange={(event) => setAlias(event.target.value)} placeholder="myapp" pattern="[a-z][a-z0-9_-]{0,63}" maxLength={64} aria-label="API alias" disabled={busy} required />
        </label>
        <fieldset><legend>Allowed tables</legend>
          {tables.length === 0 && <p>Create a table before exposing this database.</p>}
          {tables.map((table) => <label className="api-table-option" key={table.Name}>
            <input type="checkbox" checked={selected.includes(table.Name)} onChange={(event) => setSelected((current) => event.target.checked ? [...current, table.Name] : current.filter((name) => name !== table.Name))} disabled={busy} aria-label={`Expose ${table.Name}`} />
            <Icon name="table" />{table.Name}
          </label>)}
        </fieldset>
        <div className="api-access-option">
          <label><input type="checkbox" checked={writable} onChange={(event) => setWritable(event.target.checked)} disabled={busy} /> Allow writes</label>
          <p>{writable
            ? managed ? 'This token can create, edit and delete rows in the selected tables.' : 'Writes modify the original source file. Back it up and coordinate with applications using it. The discovery inspector remains read-only.'
            : 'Read-only access. No writes through this API.'}</p>
        </div>
        <button className="btn-primary" type="submit" disabled={busy || !/^[a-z][a-z0-9_-]{0,63}$/.test(alias) || selected.length === 0}><Icon name="plus" />{busy ? 'Working...' : 'Create API'}</button>
      </form>
      {credential && <div className="api-credentials">
        <label className="api-field">API token<input value={credential.token} readOnly aria-label="API token" /></label>
        <div className="api-copy-actions"><button className="btn-sm" onClick={() => void copy(credential.token)}><Icon name="copy" /> Copy token</button><button className="btn-sm" onClick={() => void copy(endpoint)}><Icon name="copy" /> Copy URL</button></div>
        <label className="api-field">Example request<textarea value={example} readOnly aria-label="Example request" rows={3} /></label>
        <div className="api-copy-actions"><button className="btn-sm" onClick={() => void copy(example)}><Icon name="copy" /> Copy curl</button><button className="btn-primary" onClick={() => setTesting(credential.exposure)} aria-label="Open request editor"><Icon name="play" /> Test API</button></div>
        <p className="api-warning">The token only grants access to these tables. Use HTTPS/TLS when accessing the daemon remotely. Never send the admin token to untrusted clients.</p>
      </div>}
      {exposures.length > 0 && <div className="api-exposure-list"><h4>Active APIs</h4>{exposures.map((entry) => <div className="api-exposure-row" key={entry.alias}>
        <div><strong>{entry.alias}</strong><span>{entry.writable ? 'Read & write' : 'Read-only'} / {entry.tables.join(', ')}</span></div>
        <button className="btn-sm" onClick={() => setTesting(entry)} aria-label={`Open request editor for ${entry.alias}`}><Icon name="terminal" /> Test API</button>
        <button className="btn-sm" disabled={busy} aria-label={`Rotate token for ${entry.alias}`} onClick={() => {
          if (!confirm(`Rotate the token for "${entry.alias}"? The previous token stops working immediately.`)) return;
          void perform(async () => {
            const result = await api.rotateExposure(entry.alias);
            setCredential({ exposure: entry, token: result.token });
            setMessage('Token rotated. Copy the new token now.');
          });
        }}><Icon name="refresh" /> Rotate token</button>
        <button className="btn-danger-sm" disabled={busy} onClick={() => revoke(entry)} aria-label={`Revoke API ${entry.alias}`} title="Revoke API"><Icon name="trash" /></button>
      </div>)}</div>}
    </section>
  );
}
