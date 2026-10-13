import { useState } from 'react';
import { api, enc, errText, get } from '../api';
import { useApp } from '../state';
import type { GitSource, Group } from '../types';
import { EXAMPLE_RULES, labelsText, parseLabels } from '../util';
import { Field, Modal, Msg } from '../components/ui';

const SOAKS = [['1m', '1 minute'], ['5m', '5 minutes'], ['15m', '15 minutes'], ['30m', '30 minutes'], ['1h', '1 hour']];
const INCREASES = [2, 3, 5, 10];

/** Create a group, or edit its settings (not its rules). */
export function GroupDialog({ group }: { group?: Group }) {
  const app = useApp();
  const ro = group?.rollout;
  const git = group?.git;
  const [f, setF] = useState({
    name: group?.name || '',
    description: group?.description || '',
    fleetOn: !group?.fleet?.disabled,
    fleetWait: group?.fleet?.wait || '5s',
    fleetWindow: group?.fleet?.window || '5m',
    opsWebhook: group?.ops_webhook_url || '',
    selector: labelsText(group?.selector),
    source: group?.source || 'manual',
    seed: 'example',
    provider: git?.provider || 'github',
    repo: git?.repo || '',
    ref: git?.ref || '',
    path: git?.provider !== 'url' ? git?.path || '' : '',
    rawurl: git?.provider === 'url' ? git.path : '',
    baseUrl: git?.base_url || '',
    gtoken: '',
    interval: git ? git.interval || '' : '5m',
    rolloutOn: !!ro?.enabled,
    stages: (ro?.stages || []).join(', '),
    soak: ro?.soak || '5m',
    maxIncrease: String(ro?.max_increase || 3),
    minAlerts: String(ro?.min_alerts || 10),
  });
  const set = <K extends keyof typeof f>(k: K, v: (typeof f)[K]) => setF(x => ({ ...x, [k]: v }));
  const [msg, setMsg] = useState<React.ReactNode>(null);
  const [busy, setBusy] = useState(false);
  const online = app.data.agents.filter(a => a.online);

  const save = async () => {
    const body: Record<string, unknown> = {
      name: f.name.trim(), description: f.description.trim(), source: f.source,
      fleet: { disabled: !f.fleetOn, wait: f.fleetWait, window: f.fleetWindow },
      ops_webhook_url: f.opsWebhook.trim(),
      rollout: f.rolloutOn
        ? { enabled: true, stages: f.stages.split(',').map(x => x.trim()).filter(Boolean), soak: f.soak, max_increase: Number(f.maxIncrease), min_alerts: parseInt(f.minAlerts, 10) || 0 }
        : { enabled: false },
    };
    try { body.selector = parseLabels(f.selector); }
    catch (e) { setMsg(<Msg kind="bad">Labels: {errText(e)}</Msg>); return; }
    if (!body.name) return;
    if (f.source === 'git') {
      const url = f.provider === 'url';
      body.git = {
        provider: f.provider, repo: url ? '' : f.repo.trim(), ref: url ? '' : f.ref.trim(),
        path: url ? f.rawurl.trim() : f.path.trim(), base_url: url ? '' : f.baseUrl.trim(), token: f.gtoken, interval: f.interval,
      } satisfies Partial<GitSource>;
    } else if (!group) {
      if (f.seed === 'example') body.rules = EXAMPLE_RULES;
      else if (f.seed.startsWith('agent:')) {
        try { body.rules = await get<string>(`/api/agents/${enc(f.seed.slice(6))}/rules`); }
        catch (e) { setMsg(<Msg kind="bad">Could not copy rules: {errText(e)}</Msg>); return; }
      }
    }
    setBusy(true);
    setMsg(f.source === 'git' ? <Msg>Saving and fetching from git…</Msg> : null);
    try {
      const { data: r } = group
        ? await api<{ name: string; sync_error?: string }>('PUT', `/api/groups/${enc(group.name)}`, body)
        : await api<{ name: string; sync_error?: string }>('POST', '/api/groups', body);
      app.closeDialog();
      if (r.sync_error) app.toast(`Group saved, but syncing from git failed: ${r.sync_error}`, true);
      else app.toast(group ? `Saved group ${r.name}` : `Created group ${r.name}`);
      await app.refresh();
      app.go({ kind: 'group', name: r.name });
    } catch (e) { setMsg(<Msg kind="bad">{errText(e)}</Msg>); }
    finally { setBusy(false); }
  };

  const isGit = f.source === 'git', isUrl = f.provider === 'url';
  const soaks = SOAKS.some(([v]) => v === f.soak) ? SOAKS : [...SOAKS, [f.soak, f.soak]];
  const increases = INCREASES.includes(Number(f.maxIncrease)) ? INCREASES : [...INCREASES, Number(f.maxIncrease)];
  const light = { fontWeight: 'normal' } as const;

  return (
    <Modal onClose={app.closeDialog}>
      <form method="dialog" autoComplete="off" onSubmit={e => { e.preventDefault(); save(); }}>
        <h2>{group ? 'Edit group ' + group.name : 'New group'}</h2>
        <div className="two">
          <Field label="Name"><input type="text" value={f.name} onChange={e => set('name', e.target.value)} placeholder="web-servers" required disabled={!!group} autoFocus={!group} /></Field>
          <Field label="Description"><input type="text" value={f.description} onChange={e => set('description', e.target.value)} placeholder="optional" autoFocus={!!group} /></Field>
        </div>

        <div className="field"><span>Alerts across agents</span>
          <label className="check"><input type="checkbox" checked={f.fleetOn} onChange={e => set('fleetOn', e.target.checked)} /> Combine the same alert from several agents into one ("on 12 hosts")</label>
          {f.fleetOn && (
            <div className="two">
              <Field label={<span className="muted" style={light}>Gather hosts for</span>}>
                <select value={f.fleetWait} onChange={e => set('fleetWait', e.target.value)}>
                  <option value="0s">0 s (send at once)</option><option value="5s">5 s</option><option value="15s">15 s</option><option value="30s">30 s</option>
                </select>
              </Field>
              <Field label={<span className="muted" style={light}>Then hold repeats for</span>}>
                <select value={f.fleetWindow} onChange={e => set('fleetWindow', e.target.value)}>
                  <option value="1m">1 minute</option><option value="5m">5 minutes</option><option value="15m">15 minutes</option><option value="1h">1 hour</option>
                </select>
              </Field>
            </div>
          )}
          <small>Applies to agents that connect to the dashboard. When the window ends, one summary says how many more matches were held back.</small>
        </div>

        <Field label={<>Agent-down alerts go to <span className="muted">(optional)</span></>} hint={<>A webhook URL for this group's agent-down and agent-back alerts. You can also enter <code>{'${ENV_VAR}'}</code>.</>}>
          <input type="text" value={f.opsWebhook} onChange={e => set('opsWebhook', e.target.value)} placeholder="default: the ops webhook in Settings" />
        </Field>

        <div className="field"><span>Rule changes</span>
          <label className="check"><input type="checkbox" checked={f.rolloutOn} onChange={e => set('rolloutOn', e.target.checked)} /> Roll out in stages: a few agents first, then more, and stop if their alerts spike</label>
          {f.rolloutOn && (
            <div className="stack">
              <div className="two">
                <Field label={<span className="muted" style={light}>Stages</span>} hint="Agent counts or percentages. The last stage is always every agent.">
                  <input type="text" value={f.stages} onChange={e => set('stages', e.target.value)} placeholder="1, 10%, 100%" />
                </Field>
                <Field label={<span className="muted" style={light}>Watch each stage for</span>}>
                  <select value={f.soak} onChange={e => set('soak', e.target.value)}>{soaks.map(([v, l]) => <option key={v} value={v}>{l}</option>)}</select>
                </Field>
              </div>
              <div className="two">
                <Field label={<span className="muted" style={light}>Stop when new-version agents alert more than</span>}>
                  <select value={f.maxIncrease} onChange={e => set('maxIncrease', e.target.value)}>{increases.map(v => <option key={v} value={String(v)}>{v}× the other agents</option>)}</select>
                </Field>
                <Field label={<span className="muted" style={light}>Ignore spikes below</span>} hint="alerts in a stage, so a few alerts never stop a rollout">
                  <input type="text" inputMode="numeric" value={f.minAlerts} onChange={e => set('minAlerts', e.target.value)} />
                </Field>
              </div>
              <small className="muted">A stopped rollout sends every agent back to the previous version and alerts the agent-down webhook. Rollbacks always go to every agent at once.</small>
            </div>
          )}
        </div>

        <Field label="Auto-add agents with labels" hint="Enrolled agents that have all of these labels join this group by themselves. Leave empty to add agents by hand.">
          <input type="text" value={f.selector} onChange={e => set('selector', e.target.value)} placeholder="role=web, env=prod" />
        </Field>

        <div className="field"><span>Rules come from</span>
          <div className="seg">
            <label><input type="radio" name="source" checked={f.source === 'manual'} onChange={() => set('source', 'manual')} /><b>This dashboard</b><small>Edit the rules here and push them to the group.</small></label>
            <label><input type="radio" name="source" checked={f.source === 'git'} onChange={() => set('source', 'git')} /><b>A git repository</b><small>Sync a rules file from GitHub or GitLab.</small></label>
          </div>
        </div>

        {!isGit && !group && (
          <Field label="Start with" hint="You can edit the rules after the group is created.">
            <select value={f.seed} onChange={e => set('seed', e.target.value)}>
              <option value="example">An example rule set</option><option value="empty">Nothing (empty)</option>
              {online.map(a => <option key={a.name} value={'agent:' + a.name}>A copy of {a.name}'s current rules</option>)}
            </select>
          </Field>
        )}

        {isGit && (
          <div className="stack">
            <div className="two">
              <Field label="Provider">
                <select value={f.provider} onChange={e => set('provider', e.target.value as GitSource['provider'])}>
                  <option value="github">GitHub</option><option value="gitlab">GitLab</option><option value="url">Raw URL</option>
                </select>
              </Field>
              {!isUrl && <Field label="Repository"><input type="text" value={f.repo} onChange={e => set('repo', e.target.value)} placeholder="my-org/grepalert-rules" /></Field>}
            </div>
            {isUrl ? (
              <Field label="Rules file URL"><input type="text" value={f.rawurl} onChange={e => set('rawurl', e.target.value)} placeholder="https://example.com/rules.yaml" /></Field>
            ) : <>
              <div className="two">
                <Field label="Branch / tag"><input type="text" value={f.ref} onChange={e => set('ref', e.target.value)} placeholder="main" /></Field>
                <Field label="File path"><input type="text" value={f.path} onChange={e => set('path', e.target.value)} placeholder="rules/web.yaml" /></Field>
              </div>
              <Field label={<>Server URL <span className="muted">(self-hosted only)</span></>}>
                <input type="text" value={f.baseUrl} onChange={e => set('baseUrl', e.target.value)} placeholder="https://gitlab.example.com or https://ghe.example.com/api/v3" />
              </Field>
            </>}
            <div className="two">
              <Field label="Access token" hint={<>Read access to the file is enough. You can also enter <code>{'${GITHUB_TOKEN}'}</code>.</>}>
                <input type="password" value={f.gtoken} onChange={e => set('gtoken', e.target.value)} placeholder={git?.has_token ? 'unchanged (enter a new token to replace it)' : 'read-only token'} />
              </Field>
              <Field label="Sync">
                <select value={f.interval} onChange={e => set('interval', e.target.value)}>
                  <option value="">Only when I click Sync</option><option value="1m">Every minute</option><option value="5m">Every 5 minutes</option>
                  <option value="15m">Every 15 minutes</option><option value="1h">Every hour</option>
                </select>
              </Field>
            </div>
          </div>
        )}

        {msg}
        <div className="foot">
          <button type="button" onClick={app.closeDialog}>Cancel</button>
          <button type="submit" className="primary" disabled={busy}>{group ? 'Save' : 'Create group'}</button>
        </div>
      </form>
    </Modal>
  );
}
