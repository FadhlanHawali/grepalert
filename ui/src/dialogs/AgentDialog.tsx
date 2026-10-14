import { useState } from 'react';
import { api, ApiError, enc, errText } from '../api';
import { useApp } from '../state';
import type { Agent, AgentStatus } from '../types';
import { Field, Modal, Msg } from '../components/ui';
import { offerOwnRules } from '../domain';

type Push = { ok: boolean; action: string; error?: string };

/** Connect an agent by address, or edit a direct agent's connection. */
export function AgentDialog({ agent, group: initialGroup }: { agent?: Agent; group?: string }) {
  const app = useApp();
  const [url, setUrl] = useState(agent?.url || '');
  const [token, setToken] = useState('');
  const [name, setName] = useState(agent?.name || '');
  const [namePlaceholder, setNamePlaceholder] = useState('from the agent');
  const [group, setGroup] = useState(agent ? agent.group || '' : initialGroup || '');
  const [insecure, setInsecure] = useState(!!agent?.insecure_skip_verify);
  const [msg, setMsg] = useState<React.ReactNode>(null);
  const [unreachable, setUnreachable] = useState(false);
  const [busy, setBusy] = useState(false);

  const body = (force = false) => ({ name: agent ? agent.name : name.trim(), url: url.trim(), token, group, insecure_skip_verify: insecure, force });

  const test = async () => {
    setMsg(<Msg>Connecting…</Msg>);
    try {
      const { data: r } = await api<{ ok: boolean; error?: string; status: AgentStatus }>('POST', '/api/agents/test', body());
      if (!r.ok) { setMsg(<Msg kind="bad">Cannot connect: {r.error}</Msg>); return; }
      const s = r.status;
      setMsg(<Msg kind="ok">Connected to "{s.name}" on host {s.host} (version {s.version}). It runs {(s.rules || []).length} rules and tails {(s.files || []).length} files.</Msg>);
      if (!agent && !name) setNamePlaceholder(s.name);
    } catch (e) { setMsg(<Msg kind="bad">{errText(e)}</Msg>); }
  };

  const save = async (force = false) => {
    if (!url.trim()) return;
    const g = group ? app.groupOf(group) : undefined;
    if (agent && group && group !== agent.group && g?.rules_hash &&
      !confirm(`Group "${group}" will replace this agent's current rules with the group's rules. Continue?`)) return;
    setBusy(true);
    try {
      const { data: r } = agent
        ? await api<{ name: string; push?: Push }>('PUT', `/api/agents/${enc(agent.name)}`, body(force))
        : await api<{ name: string; push?: Push }>('POST', '/api/agents', body(force));
      app.closeDialog();
      let note = agent ? `Saved ${r.name}` : `Connected ${r.name}`;
      if (r.push) note += r.push.ok ? ` · group rules ${r.push.action}` : ` · pushing group rules failed: ${r.push.error}`;
      app.toast(note, !!r.push && !r.push.ok);
      if (agent && !group && agent.group) await offerOwnRules(agent, agent.group, app.toast);
      await app.refresh();
      // Show the agent where it now lives, with its details open.
      app.go(group ? { kind: 'group', name: group } : { kind: app.view.kind === 'ungrouped' ? 'ungrouped' : 'all' });
      app.selectAgent(r.name);
    } catch (e) {
      const unreach = e instanceof ApiError && !!(e.data as { unreachable?: boolean })?.unreachable;
      setUnreachable(unreach);
      setMsg(<Msg kind="bad">{errText(e)}</Msg>);
    } finally { setBusy(false); }
  };

  return (
    <Modal onClose={app.closeDialog}>
      <form method="dialog" autoComplete="off" onSubmit={e => { e.preventDefault(); save(); }}>
        <h2>{agent ? 'Edit agent ' + agent.name : 'Connect agent'}</h2>
        {!agent && (
          <Msg style={{ margin: 0 }}>Servers behind NAT or a firewall? Agents can connect to the dashboard themselves, with no inbound port.{' '}
            <button type="button" className="link" onClick={() => { app.closeDialog(); app.go({ kind: 'enroll' }); }}>Create an enrollment token</button></Msg>
        )}
        <Field label="Agent URL" hint={<>Where the agent's API listens (its <code>listen</code> setting).</>}>
          <input type="text" value={url} onChange={e => setUrl(e.target.value)} placeholder="http://10.0.0.5:9300" required autoFocus />
        </Field>
        <Field label="Token" hint={<>The <code>token</code> from the agent's config. You can also enter <code>{'${ENV_VAR}'}</code>.</>}>
          <input type="password" value={token} onChange={e => setToken(e.target.value)}
            placeholder={agent?.has_token ? 'unchanged (enter a new token to replace it)' : "the agent's token"} />
        </Field>
        <div className="two">
          <Field label="Name" hint="Leave empty to use the agent's own name.">
            <input type="text" value={name} onChange={e => setName(e.target.value)} placeholder={namePlaceholder} disabled={!!agent} />
          </Field>
          <Field label="Group" hint="The group's rules are pushed to it.">
            <select value={group} onChange={e => setGroup(e.target.value)}>
              <option value="">No group (own rules)</option>
              {app.data.groups.map(g => <option key={g.name} value={g.name}>{g.name}</option>)}
            </select>
          </Field>
        </div>
        <label className="check"><input type="checkbox" checked={insecure} onChange={e => setInsecure(e.target.checked)} /> Skip TLS certificate verification (self-signed HTTPS)</label>
        {msg}
        {unreachable && (
          <div className="row"><span className="muted">Is the agent not running yet?</span>
            <button type="button" onClick={() => save(true)}>Save anyway</button></div>
        )}
        <div className="foot">
          <button type="button" onClick={test}>Test connection</button>
          <span className="spacer" />
          <button type="button" onClick={app.closeDialog}>Cancel</button>
          <button type="submit" className="primary" disabled={busy}>{agent ? 'Save' : 'Connect'}</button>
        </div>
      </form>
    </Modal>
  );
}
