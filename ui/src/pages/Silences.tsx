import { useState } from 'react';
import { api, enc, errText } from '../api';
import { useApp } from '../state';
import { scopeText } from '../domain';
import type { Silence } from '../types';
import { splitList, until } from '../util';
import { Card, Field } from '../components/ui';

/** Group to preselect when opening the page from a group's "Silence…" button. */
let preselect = '';
export const silenceGroup = (name: string) => { preselect = name; };

export function SilencesPage() {
  const app = useApp();
  const groups = app.data.groups.filter(g => app.can('editor', g.name));
  const [group, setGroup] = useState(() => {
    const g = preselect; preselect = '';
    return g || (app.can('editor', '') ? '' : groups[0]?.name || '');
  });
  const [duration, setDuration] = useState('2h');
  const [rules, setRules] = useState('');
  const [agents, setAgents] = useState('');
  const [reason, setReason] = useState('');

  const create = async () => {
    try {
      await api('POST', '/api/silences', { group, duration, rules: splitList(rules), agents: splitList(agents), reason: reason.trim() });
      app.toast('Silence created');
      setRules(''); setAgents(''); setReason('');
      app.refresh();
    } catch (e) { app.toast(errText(e), true); }
  };
  const end = async (id: string) => {
    try { await api('DELETE', `/api/silences/${enc(id)}`); app.toast('Silence ended'); app.refresh(); }
    catch (e) { app.toast(errText(e), true); }
  };

  const now = Date.now();
  const active = app.silences.filter(s => new Date(s.ends_at).getTime() > now);
  const past = app.silences.filter(s => new Date(s.ends_at).getTime() <= now).slice(0, 20);
  const table = (list: Silence[], isActive: boolean) => (
    <div className="scroll"><table>
      <thead><tr><th>Scope</th><th>Reason</th><th>Started</th><th>{isActive ? 'Ends' : 'Ended'}</th><th></th></tr></thead>
      <tbody>{list.map(s => (
        <tr key={s.id}>
          <td>{scopeText(s)}</td><td className="wrap">{s.reason || '—'}</td>
          <td>{new Date(s.starts_at).toLocaleString()}</td>
          <td>{isActive ? 'in ' + until(s.ends_at) : new Date(s.ends_at).toLocaleString()}</td>
          <td>{isActive && <button onClick={() => end(s.id)}>End now</button>}</td>
        </tr>
      ))}</tbody>
    </table></div>
  );

  return (
    <Card title="Silences" sub="Mute alerts during maintenance. Silenced alerts are not sent but still appear in the alert history. Agents also receive silences, so they hold even when an agent has to send alerts itself.">
      <div className="pane" style={{ paddingTop: 0 }}>
        <div className="stack" style={{ maxWidth: 720 }}>
          <div className="two">
            <Field label="Group">
              <select value={group} onChange={e => setGroup(e.target.value)}>
                {app.can('editor', '') && <option value="">All groups</option>}
                {groups.map(g => <option key={g.name}>{g.name}</option>)}
              </select>
            </Field>
            <Field label="For">
              <select value={duration} onChange={e => setDuration(e.target.value)}>
                {[['30m', '30 minutes'], ['1h', '1 hour'], ['2h', '2 hours'], ['4h', '4 hours'], ['8h', '8 hours'], ['24h', '1 day'], ['72h', '3 days']].map(([v, l]) => <option key={v} value={v}>{l}</option>)}
              </select>
            </Field>
          </div>
          <div className="two">
            <Field label={<>Only rules <span className="muted">(optional)</span></>} hint={<>Comma-separated. Empty = all rules. <code>agent-down</code> silences agent-down alerts.</>}>
              <input type="text" value={rules} onChange={e => setRules(e.target.value)} placeholder="errors, panics, agent-down" />
            </Field>
            <Field label={<>Only agents <span className="muted">(optional)</span></>} hint="Comma-separated. Empty = all agents.">
              <input type="text" value={agents} onChange={e => setAgents(e.target.value)} placeholder="db-01, db-02" />
            </Field>
          </div>
          <Field label="Reason"><input type="text" value={reason} onChange={e => setReason(e.target.value)} placeholder="kernel patching, ticket OPS-123" /></Field>
          <div><button className="primary" onClick={create}>Create silence</button></div>
        </div>
        <h3 className="sec">Active</h3>
        {active.length ? table(active, true) : <div className="muted">None.</div>}
        <h3 className="sec">Recently ended</h3>
        {past.length ? table(past, false) : <div className="muted">None.</div>}
      </div>
    </Card>
  );
}
