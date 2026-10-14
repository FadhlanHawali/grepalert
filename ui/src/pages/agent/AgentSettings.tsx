import { useState } from 'react';
import { api, enc, errText } from '../../api';
import { useApp } from '../../state';
import type { Agent } from '../../types';
import { ago, zero } from '../../util';
import { LabelChips } from '../../components/AgentTable';
import { offerOwnRules } from '../../domain';

/** Admin settings of an agent: connection, group, revoke, remove. */
export function AgentSettings({ a }: { a: Agent }) {
  const app = useApp();

  const remove = async () => {
    if (!confirm(`Remove ${a.name} from the dashboard? The agent keeps running with its current rules.`)) return;
    try { await api('DELETE', `/api/agents/${enc(a.name)}`); app.toast(`Removed ${a.name}`); app.selectAgent(null); app.refresh(); }
    catch (e) { app.toast(errText(e), true); }
  };

  if (a.mode !== 'connected') {
    return (
      <>
        <div className="grid" style={{ gridTemplateColumns: 'repeat(auto-fill,minmax(220px,1fr))' }}>
          <div className="stat"><div className="k">URL</div><div className="mono">{a.url}</div></div>
          <div className="stat"><div className="k">Group</div><div>{a.group || <span className="muted">none</span>}</div></div>
          <div className="stat"><div className="k">Token</div><div>{a.has_token ? 'set' : <span className="muted">none</span>}</div></div>
          <div className="stat"><div className="k">Connected</div><div>{ago(a.added_at)}</div></div>
        </div>
        <div className="row">
          <button className="primary" onClick={() => app.openDialog({ kind: 'agent', agent: a })}>Edit connection / group</button>
          <span className="spacer" />
          <button className="danger" onClick={remove}>Remove from dashboard</button>
        </div>
        <p className="muted">Removing only disconnects the dashboard. The agent keeps running with its current rules.</p>
      </>
    );
  }
  return <ConnectedSettings a={a} remove={remove} />;
}

function ConnectedSettings({ a, remove }: { a: Agent; remove: () => void }) {
  const app = useApp();
  const [group, setGroup] = useState(a.assigned ? a.group || '' : '');
  const byLabels = app.data.groups.find(g => g.selector && Object.entries(g.selector).every(([k, v]) => (a.labels || {})[k] === v));
  const stat = (k: string, v: React.ReactNode) => <div className="stat"><div className="k">{k}</div><div>{v}</div></div>;

  const saveGroup = async () => {
    if (group && group !== a.group && app.groupOf(group)?.rules_hash &&
      !confirm(`Group "${group}" will replace this agent's current rules with the group's rules. Continue?`)) return;
    try {
      const { data: r } = await api<{ push?: { ok: boolean; action: string; error?: string } }>('PUT', `/api/agents/${enc(a.name)}`, { group });
      app.toast(`Saved ${a.name}` + (r.push ? (r.push.ok ? ` · group rules ${r.push.action}` : ` · push failed: ${r.push.error}`) : ''), r.push && !r.push.ok);
      // Only an explicit "no group" can leave: "automatic" may match a group by label.
      if (!group && !r.push && a.group) await offerOwnRules(a, a.group, app.toast);
      app.refresh();
    } catch (e) { app.toast(errText(e), true); }
  };
  const setRevoked = async (revoke: boolean) => {
    if (revoke && !confirm(`Revoke ${a.name}? The dashboard will refuse its connection and stop managing it. The agent keeps running with its current rules.`)) return;
    try {
      await api('POST', `/api/agents/${enc(a.name)}/${revoke ? 'revoke' : 'allow'}`);
      app.toast(revoke ? `Revoked ${a.name}` : `${a.name} allowed again; it reconnects within 15 seconds`);
      app.refresh();
    } catch (e) { app.toast(errText(e), true); }
  };

  return (
    <>
      <div className="grid" style={{ gridTemplateColumns: 'repeat(auto-fill,minmax(220px,1fr))' }}>
        {stat('Connection', '⇡ connects out to the dashboard (no inbound port)')}
        {stat('Hostname', a.hostname || '—')}
        {stat('Last seen', a.online ? 'connected now' : zero(a.last_seen) ? 'not since the dashboard started' : ago(a.last_seen))}
        {stat('Enrolled', <>{ago(a.added_at)} with token <b>{a.enrolled_via || '—'}</b></>)}
        {stat('Labels', a.labels && Object.keys(a.labels).length ? <LabelChips labels={a.labels} /> : <span className="muted">none</span>)}
      </div>
      <label className="field" style={{ maxWidth: 420 }}>
        <span>Group</span>
        <select value={group} onChange={e => setGroup(e.target.value)}>
          <option value="">Automatic by labels{byLabels ? ` (currently ${byLabels.name})` : ' (no group matches)'}</option>
          {app.data.groups.map(g => <option key={g.name} value={g.name}>{g.name}</option>)}
        </select>
        <small>Pick a group to override the labels. The group's rules replace the agent's rules.</small>
      </label>
      <div className="row">
        <button className="primary" onClick={saveGroup}>Save group</button>
        <span className="spacer" />
        {a.revoked ? <button onClick={() => setRevoked(false)}>Allow again</button> : <button className="danger" onClick={() => setRevoked(true)}>Revoke access</button>}
        <button className="danger" onClick={remove}>Remove</button>
      </div>
      <p className="muted"><b>Revoke</b> blocks this agent: the dashboard refuses its connection and stops managing it, until you allow it again.{' '}
        <b>Remove</b> deletes it from the list; if it still has a valid enrollment token it will enroll again.</p>
    </>
  );
}
