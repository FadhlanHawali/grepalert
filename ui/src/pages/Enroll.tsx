import { useState } from 'react';
import { api, enc, errText, get } from '../api';
import { useApp, useLoad } from '../state';
import { ago, zero } from '../util';
import { Badge, Card, Field, Msg } from '../components/ui';

interface Token { id: string; name: string; prefix: string; created_at: string; expires_at?: string; revoked?: boolean; expired?: boolean; uses: number; last_used?: string }

function agentSnippet(origin: string, token: string) {
  return `# /etc/grepalert/agent.yaml
dashboard:
  url: ${origin}
  enrollment_token: ${token}
labels:            # groups can auto-add agents by label
  role: web
  env: prod
rules_file: /var/lib/grepalert/rules.yaml
# name: web-01     # defaults to the hostname`;
}

export function EnrollPage() {
  const app = useApp();
  const origin = location.origin;
  const [name, setName] = useState('');
  const [ttl, setTtl] = useState('24h');
  const [created, setCreated] = useState<{ name: string; token: string } | null>(null);
  const tokens = useLoad(() => get<{ tokens: Token[]; config_token: boolean }>('/api/tokens'), []);

  const create = async () => {
    if (!name.trim()) return;
    try {
      const { data } = await api<{ name: string; token: string }>('POST', '/api/tokens', { name: name.trim(), ttl });
      setCreated(data); setName(''); tokens.reload();
    } catch (e) { app.toast(errText(e), true); }
  };
  const revoke = async (id: string) => {
    if (!confirm('Revoke this token? New agents can no longer enroll with it. Agents that already enrolled keep working.')) return;
    try { await api('DELETE', `/api/tokens/${enc(id)}`); app.toast('Token revoked'); tokens.reload(); }
    catch (e) { app.toast(errText(e), true); }
  };
  const copy = async (text: string) => {
    try { await navigator.clipboard.writeText(text); app.toast('Copied'); }
    catch { app.toast('Copy failed: select the text and copy it manually', true); }
  };

  const list = tokens.value?.tokens.slice().reverse() || [];
  return (
    <Card title="Agent enrollment" sub={<>Agents enroll with a token, then keep an outbound connection to this dashboard. Your servers need no inbound port, and it works behind NAT and HTTP proxies.
      Give agents <b>labels</b> and groups can add them automatically.</>}>
      <div className="pane" style={{ paddingTop: 0 }}>
        <div className="row" style={{ margin: 0, alignItems: 'flex-end' }}>
          <Field label="Token name" style={{ flex: 1, minWidth: 200 }}>
            <input type="text" value={name} onChange={e => setName(e.target.value)} placeholder="prod-web-images" onKeyDown={e => e.key === 'Enter' && create()} />
          </Field>
          <Field label="Expires" style={{ width: 200 }}>
            <select value={ttl} onChange={e => setTtl(e.target.value)}>
              <option value="">Never</option><option value="1h">In 1 hour</option><option value="24h">In 1 day</option>
              <option value="168h">In 7 days</option><option value="720h">In 30 days</option>
            </select>
          </Field>
          <button className="primary" onClick={create}>Create token</button>
        </div>
        {created && (
          <div className="msg ok" style={{ fontFamily: 'inherit', whiteSpace: 'normal' }}>
            <b>Token "{created.name}" created.</b> Copy it now: it will not be shown again.
            <div className="row">
              <code style={{ userSelect: 'all', wordBreak: 'break-all', flex: 1 }}>{created.token}</code>
              <button onClick={() => copy(created.token)}>Copy token</button>
              <button onClick={() => copy(agentSnippet(origin, created.token))}>Copy agent config</button>
            </div>
          </div>
        )}
        <h3 className="sec">Tokens</h3>
        {tokens.error ? <Msg kind="bad">{tokens.error}</Msg> : !tokens.value ? <div className="muted">Loading…</div> : <>
          {tokens.value.config_token && <Msg style={{ margin: '0 0 10px' }}>A fixed token is also set in the dashboard config (<code>enrollment_token</code>), for automation. Agents enrolled with it show "config".</Msg>}
          {list.length ? (
            <div className="scroll"><table>
              <thead><tr><th>Name</th><th>Token</th><th>Created</th><th>Expires</th><th className="num">Agents enrolled</th><th>Last used</th><th>Status</th><th></th></tr></thead>
              <tbody>{list.map(t => (
                <tr key={t.id}>
                  <td><b>{t.name}</b></td><td className="mono">{t.prefix}…</td><td>{ago(t.created_at)}</td>
                  <td>{zero(t.expires_at) ? 'never' : new Date(t.expires_at!).toLocaleString()}</td>
                  <td className="num">{t.uses}</td><td>{zero(t.last_used) ? '—' : ago(t.last_used)}</td>
                  <td>{t.revoked ? <Badge kind="bad">revoked</Badge> : t.expired ? <Badge kind="warn">expired</Badge> : <Badge kind="ok">active</Badge>}</td>
                  <td>{!t.revoked && <button className="danger" onClick={() => revoke(t.id)}>Revoke</button>}</td>
                </tr>
              ))}</tbody>
            </table></div>
          ) : <div className="empty" style={{ padding: 16 }}>No tokens yet.</div>}
        </>}
        <h3 className="sec">How an agent connects</h3>
        <pre className="preview mono">{agentSnippet(origin, '<enrollment token>')}</pre>
        <p className="muted">The token is only used once. On first contact the agent receives its own credential and saves it to <code>identity_file</code>.
          Revoking a token stops new enrollments; agents that already enrolled keep working. To cut off an agent, revoke the agent itself.</p>
      </div>
    </Card>
  );
}
