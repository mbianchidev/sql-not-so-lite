import { useEffect, useState, type ReactNode } from 'react';
import { api, onAuthenticationRequired, setAdminToken } from '../api/client';
import { Icon } from './Icon';

export function AuthenticationGate({ children }: { children: ReactNode }) {
  const [required, setRequired] = useState<boolean | null>(null);
  const [authenticated, setAuthenticated] = useState(false);
  const [token, setToken] = useState('');
  const [error, setError] = useState<string | null>(null);
  const [checking, setChecking] = useState(false);
  const [attempt, setAttempt] = useState(0);

  useEffect(() => onAuthenticationRequired(() => {
    setAuthenticated(false);
    setRequired(true);
  }), []);

  useEffect(() => {
    let active = true;
    void api.getAuthentication().then((response) => {
      if (active) { setRequired(response.required); setError(null); }
    }).catch((err: unknown) => {
      if (active) setError(err instanceof Error ? err.message : 'Unable to check authentication.');
    });
    return () => { active = false; };
  }, [attempt]);

  const signIn = async () => {
    if (!token.trim() || checking) return;
    setChecking(true);
    setError(null);
    setAdminToken(token.trim());
    try {
      await api.getHealth();
      setAuthenticated(true);
      setToken('');
    } catch (err) {
      setAdminToken(null);
      setError(err instanceof Error ? err.message : 'Sign-in failed.');
    } finally {
      setChecking(false);
    }
  };

  if (required === false || authenticated) return children;
  return (
    <main className="auth-screen">
      <div className="auth-card">
        <div className="logo"><span className="logo-icon"><Icon name="database" /></span><h1>sql-not-so-lite</h1></div>
        <h2>{required === null ? 'Connecting to your workspace' : 'Unlock your workspace'}</h2>
        {error && <div className="error-msg" role="alert">{error}</div>}
        {required === null ? (
          error ? <button className="btn-sm" onClick={() => setAttempt((value) => value + 1)}>Retry connection</button>
            : <div className="loading" role="status">Checking access...</div>
        ) : (
          <form autoComplete="off" onSubmit={(event) => { event.preventDefault(); void signIn(); }}>
            <p>Enter the admin token configured for this daemon. It stays in memory and is cleared on reload or sign-out.</p>
            <label htmlFor="admin-token">Admin token</label>
            <input id="admin-token" type="password" value={token} onChange={(event) => setToken(event.target.value)} autoComplete="off" disabled={checking} required />
            <button className="btn-primary" disabled={checking || !token.trim()}><Icon name="lock" />{checking ? 'Signing in...' : 'Sign in'}</button>
          </form>
        )}
      </div>
    </main>
  );
}
