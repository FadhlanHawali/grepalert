import { useEffect, useState } from 'react';
import { api, errText, get } from '../api';

/** The sign-in page: SSO button and/or local username and password. */
export function Login() {
  const params = new URLSearchParams(location.search);
  const next = params.get('next') || '/';
  const [cfg, setCfg] = useState<{ oidc?: string; local?: boolean } | null>(null);
  const [error, setError] = useState(params.get('error') || '');
  const [user, setUser] = useState('');
  const [pass, setPass] = useState('');
  const [busy, setBusy] = useState(false);

  useEffect(() => {
    document.title = 'Sign in · grepalert';
    get<{ oidc?: string; local?: boolean }>('/api/auth/config').then(setCfg).catch(() => setError('Cannot reach the dashboard.'));
  }, []);

  const submit = async (e: React.FormEvent) => {
    e.preventDefault();
    setBusy(true);
    try {
      await api('POST', '/auth/local', { username: user.trim(), password: pass });
      // Only same-site paths, never an absolute URL.
      location.href = next.startsWith('/') && !next.startsWith('//') ? next : '/';
    } catch (err) {
      setError(errText(err) || 'Sign-in failed.');
      setPass('');
    } finally { setBusy(false); }
  };

  return (
    <div className="login-page">
      <main className="login-box">
        <h1><span>grep</span>alert</h1>
        <p className="muted">Sign in to the dashboard</p>
        {error && <div className="msg bad">{error}</div>}
        {cfg?.oidc && <a className="button primary sso" href={'/auth/oidc?next=' + encodeURIComponent(next)} autoFocus={!cfg.local}>Sign in with {cfg.oidc}</a>}
        {cfg?.oidc && cfg.local && <div className="or"><span>or</span></div>}
        {cfg?.local && (
          <form onSubmit={submit} autoComplete="on">
            <label className="field"><span>Username</span><input type="text" value={user} onChange={e => setUser(e.target.value)} autoComplete="username" required autoFocus /></label>
            <label className="field"><span>Password</span><input type="password" value={pass} onChange={e => setPass(e.target.value)} autoComplete="current-password" required /></label>
            <button type="submit" className="primary" disabled={busy}>Sign in</button>
          </form>
        )}
        {cfg && !cfg.oidc && !cfg.local && <p className="muted">No sign-in method is configured.</p>}
      </main>
    </div>
  );
}
