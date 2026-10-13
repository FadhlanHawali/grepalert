import { useState } from 'react';
import { api, enc, errText, get } from '../api';
import { useApp, useLoad } from '../state';
import type { Role } from '../types';
import { ago } from '../util';
import { Badge, Card, Field, Msg } from '../components/ui';

interface Binding { id: string; subject: string; role: Role; group?: string; created_at: string; created_by?: string }
interface Users { builtin_admin?: string; users: { username: string; created_at: string }[] }

export function AccessPage() {
  const app = useApp();
  const users = useLoad(() => get<Users>('/api/users'), []);
  const bindings = useLoad(() => get<{ bindings: Binding[]; config_admins: string[] }>('/api/access'), []);
  const [kind, setKind] = useState('user');
  const [subject, setSubject] = useState('');
  const [role, setRole] = useState<Role>('viewer');
  const [group, setGroup] = useState('');
  const [uname, setUname] = useState('');
  const [pass, setPass] = useState('');

  const grant = async () => {
    if (!subject.trim()) return;
    try {
      await api('POST', '/api/access', { subject: `${kind}:${subject.trim()}`, role, group: role === 'admin' ? '' : group });
      app.toast('Access granted'); setSubject(''); bindings.reload();
    } catch (e) { app.toast(errText(e), true); }
  };
  const revoke = async (id: string) => {
    if (!confirm('Revoke this role?')) return;
    try { await api('DELETE', `/api/access/${enc(id)}`); app.toast('Access revoked'); bindings.reload(); }
    catch (e) { app.toast(errText(e), true); }
  };
  const saveUser = async () => {
    const name = uname.trim();
    if (!name) return;
    try {
      const { data: r } = await api<{ created: boolean }>('PUT', `/api/users/${enc(name)}`, { password: pass });
      app.toast(r.created ? `Created ${name}. Grant a role to user:${name} so it can see something.` : `Password of ${name} reset; their sessions were ended`);
      setPass(''); users.reload();
    } catch (e) { app.toast(errText(e), true); }
  };
  const deleteUser = async (name: string) => {
    if (!confirm(`Delete local user ${name}? Their sessions end immediately.`)) return;
    try { await api('DELETE', `/api/users/${enc(name)}`); app.toast('User deleted'); users.reload(); }
    catch (e) { app.toast(errText(e), true); }
  };

  const error = users.error || bindings.error;
  return (
    <Card title="Users & access" sub={<>Roles: <b>viewer</b> sees a group, <b>editor</b> also edits its rules, syncs and silences it,{' '}
      <b>admin</b> manages everything (agents, groups, enrollment, settings, users, audit log).
      Grant roles to a user (email for SSO, username for local accounts) or to a group from your identity provider.</>}>
      <div className="pane" style={{ paddingTop: 0 }}>
        {error ? <Msg kind="bad">{error}</Msg> : !users.value || !bindings.value ? 'Loading…' : <>
          <h3 className="sec" style={{ marginTop: 0 }}>Role bindings</h3>
          <div className="row" style={{ margin: '0 0 10px', alignItems: 'flex-end' }}>
            <Field label="Who" style={{ width: 120 }}>
              <select value={kind} onChange={e => setKind(e.target.value)}><option value="user">User</option><option value="group">IdP group</option></select>
            </Field>
            <Field label={' '} style={{ flex: 1, minWidth: 180 }}>
              <input type="text" value={subject} onChange={e => setSubject(e.target.value)} placeholder="dana@example.com  or  db-team" />
            </Field>
            <Field label="Role" style={{ width: 120 }}>
              <select value={role} onChange={e => setRole(e.target.value as Role)}><option>viewer</option><option>editor</option><option>admin</option></select>
            </Field>
            <Field label="On" style={{ width: 170 }}>
              <select value={group} onChange={e => setGroup(e.target.value)} disabled={role === 'admin'}>
                <option value="">All groups</option>
                {app.data.groups.map(g => <option key={g.name}>{g.name}</option>)}
              </select>
            </Field>
            <button className="primary" onClick={grant}>Grant</button>
          </div>
          <div className="scroll"><table>
            <thead><tr><th>Who</th><th>Role</th><th>On</th><th>Granted</th><th></th></tr></thead>
            <tbody>
              {(bindings.value.config_admins || []).map(a => (
                <tr key={'cfg' + a}><td>{a}</td><td><Badge kind="ok">admin</Badge></td><td>all groups</td><td className="muted">dashboard config file</td><td></td></tr>
              ))}
              {(bindings.value.bindings || []).map(x => (
                <tr key={x.id}>
                  <td>{x.subject}</td><td><Badge>{x.role}</Badge></td><td>{x.group || 'all groups'}</td>
                  <td className="muted">{ago(x.created_at)}{x.created_by && ' by ' + x.created_by}</td>
                  <td><button className="danger" onClick={() => revoke(x.id)}>Revoke</button></td>
                </tr>
              ))}
            </tbody>
          </table></div>

          <h3 className="sec">Local accounts</h3>
          <p className="muted" style={{ margin: '0 0 10px' }}>For teams without SSO, and for break-glass access. Passwords need at least 12 characters.</p>
          <div className="row" style={{ margin: '0 0 10px', alignItems: 'flex-end' }}>
            <Field label="Username" style={{ width: 200 }}><input type="text" value={uname} onChange={e => setUname(e.target.value)} autoComplete="off" /></Field>
            <Field label="Password" style={{ width: 220 }}><input type="password" value={pass} onChange={e => setPass(e.target.value)} autoComplete="new-password" /></Field>
            <button onClick={saveUser}>Create / reset password</button>
          </div>
          <div className="scroll"><table>
            <thead><tr><th>Username</th><th>Created</th><th></th></tr></thead>
            <tbody>
              {users.value.builtin_admin && <tr><td>{users.value.builtin_admin}</td><td className="muted">built-in admin from the config file</td><td></td></tr>}
              {(users.value.users || []).map(x => (
                <tr key={x.username}><td>{x.username}</td><td>{ago(x.created_at)}</td>
                  <td><button className="danger" onClick={() => deleteUser(x.username)}>Delete</button></td></tr>
              ))}
            </tbody>
          </table></div>
        </>}
      </div>
    </Card>
  );
}
