// Sign-in page. Loaded as a separate file so the strict Content-Security-Policy
// (no inline scripts) applies to this page too.
(async () => {
  const $ = s => document.querySelector(s);
  const params = new URLSearchParams(location.search);
  const next = params.get('next') || '/';

  const showError = text => { const e = $('#login-error'); e.textContent = text; e.hidden = false; };
  if (params.get('error')) showError(params.get('error'));

  let cfg = {};
  try { cfg = await (await fetch(location.origin + '/api/auth/config')).json(); }
  catch { showError('Cannot reach the dashboard.'); return; }

  if (cfg.oidc) {
    const a = $('#sso');
    a.textContent = 'Sign in with ' + cfg.oidc;
    a.href = '/auth/oidc?next=' + encodeURIComponent(next);
    a.hidden = false;
  }
  if (cfg.local) $('#local').hidden = false;
  if (cfg.oidc && cfg.local) $('#or').hidden = false;
  if (!cfg.oidc && !cfg.local) $('#none').hidden = false;
  (cfg.local ? $('#local [name=username]') : $('#sso'))?.focus();

  $('#local').addEventListener('submit', async e => {
    e.preventDefault();
    const f = e.target, btn = f.querySelector('button');
    btn.disabled = true;
    try {
      const r = await fetch(location.origin + '/auth/local', {
        method: 'POST',
        headers: { 'Content-Type': 'application/json', 'X-Requested-With': 'grepalert' },
        body: JSON.stringify({ username: f.username.value.trim(), password: f.password.value }),
      });
      if (r.ok) {
        // Only same-site paths, never an absolute URL.
        location.href = next.startsWith('/') && !next.startsWith('//') ? next : '/';
        return;
      }
      const data = await r.json().catch(() => ({}));
      showError(data.error || 'Sign-in failed.');
      f.password.value = '';
      f.password.focus();
    } catch { showError('Cannot reach the dashboard.'); }
    finally { btn.disabled = false; }
  });
})();
