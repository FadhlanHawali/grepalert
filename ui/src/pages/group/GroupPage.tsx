import { useState } from 'react';
import { api, enc, errText } from '../../api';
import { useApp } from '../../state';
import { pushSummary, sourceText, stageText, stagesOf, syncState } from '../../domain';
import type { Agent, Group, GroupState } from '../../types';
import { ago, labelsText, zero } from '../../util';
import { AgentTable } from '../../components/AgentTable';
import { SilenceBanner } from '../../components/SilenceBanner';
import { silenceGroup } from '../Silences';
import { Card, Msg, Tabs } from '../../components/ui';
import { GroupHistory } from './GroupHistory';
import { GroupRules } from './GroupRules';
import { RolloutPanel } from './RolloutPanel';

type Tab = 'agents' | 'rules' | 'history';

export function GroupPage({ g }: { g: Group }) {
  const app = useApp();
  const [tab, setTab] = useState<Tab>('agents');
  const [syncing, setSyncing] = useState(false);
  const st = g.state;
  const members = g.members.map(n => app.agentOf(n)).filter((a): a is Agent => !!a);
  const inSync = members.filter(a => syncState(a, g)?.cls === 'ok').length;
  const editor = app.can('editor', g.name);

  const sync = async () => {
    setSyncing(true);
    try {
      const { data } = await api<GroupState>('POST', `/api/groups/${enc(g.name)}/sync`);
      app.toast('Synced: ' + pushSummary(data.results).replace(/\n/g, ' · '));
    } catch (e) { app.toast(errText(e), true); }
    setSyncing(false);
    app.refresh();
  };

  const remove = async () => {
    if (!confirm(`Delete group ${g.name}? Its ${g.members.length} agent(s) become ungrouped and keep their current rules.`)) return;
    try { await api('DELETE', `/api/groups/${enc(g.name)}`); app.toast(`Deleted group ${g.name}`); await app.refresh(); app.go({ kind: 'all' }); }
    catch (e) { app.toast(errText(e), true); }
  };

  const fleet = g.fleet?.disabled ? 'off (every agent alerts separately)'
    : `one alert across hosts, gathered ${g.fleet?.wait || '5s'}, repeats held ${g.fleet?.window || '5m'}`;

  return (
    <Card
      title={g.name}
      sub={g.description}
      actions={<>
        {editor && <>
          <button disabled={syncing || st.syncing} onClick={sync}>{syncing || st.syncing ? 'Syncing…' : g.source === 'git' ? 'Sync from git' : 'Push to agents'}</button>
          <button onClick={() => { silenceGroup(g.name); app.go({ kind: 'silences' }); }}>Silence…</button>
        </>}
        {app.isAdmin && <>
          <button onClick={() => app.openDialog({ kind: 'group', group: g })}>Edit</button>
          <button className="danger" onClick={remove}>Delete</button>
        </>}
      </>}
    >
      <div className="meta">
        <span>Rules: <b>{sourceText(g)}</b></span>
        <span>Fleet: <b>{fleet}</b></span>
        <span>Members: <b>{g.selector ? 'agents labelled ' + labelsText(g.selector) + ', plus any added by hand' : 'added by hand'}</b></span>
        {g.source === 'git' && <span>Sync: <b>{g.git?.interval ? 'every ' + g.git.interval : 'manual'}</b></span>}
        <span>Version: <b className="mono">{g.rules_hash || 'none yet'}</b>{g.rules_hash && ' · updated ' + ago(g.rules_updated_at)}</span>
        <span>Changes: <b>{g.rollout?.enabled ? `staged (${stagesOf(g).map(stageText).join(' → ')}, ${g.rollout.soak} each)` : 'to every agent at once'}</b></span>
        <span>Agents in sync: <b>{inSync}/{members.length}</b></span>
        {!zero(st.last_sync) && <span>Last sync: <b>{ago(st.last_sync)}</b> ({st.trigger})</span>}
      </div>
      <SilenceBanner group={g.name} />
      <RolloutPanel g={g} />
      {g.git_paused && (
        <div className="pane" style={{ paddingTop: 0 }}>
          <Msg style={{ margin: 0 }}>⏸ <b>Not following git:</b> {g.git_paused}{editor && <> <button className="link" onClick={sync}>Sync from git now</button></>}</Msg>
        </div>
      )}
      {st.last_error && <div className="pane" style={{ paddingTop: 0 }}><Msg kind="bad">{st.last_error}</Msg></div>}
      {!g.rules_hash && g.source === 'manual' && (
        <div className="pane" style={{ paddingTop: 0 }}><Msg>This group has no rules yet. Open the <b>Rules</b> tab to write them.</Msg></div>
      )}
      <Tabs<Tab> value={tab} onChange={setTab} tabs={[
        { id: 'agents', label: `Agents (${members.length})` },
        { id: 'rules', label: 'Rules' },
        { id: 'history', label: 'History' },
      ]} />
      {tab === 'agents' && <>
        {members.length > 0 && app.isAdmin && <>
          <div className="pane" style={{ paddingBottom: 0 }}>
            <div className="row" style={{ margin: 0 }}>
              <button className="primary" onClick={() => app.openDialog({ kind: 'agent', group: g.name })}>Connect agent</button>
              {app.data.agents.some(a => a.group !== g.name) && <button onClick={() => app.openDialog({ kind: 'add-existing', group: g.name })}>Add an existing agent</button>}
            </div>
          </div>
          <div style={{ height: 12 }} />
        </>}
        <AgentTable list={members} group={g.name} />
      </>}
      {tab === 'rules' && <GroupRules g={g} />}
      {tab === 'history' && <GroupHistory g={g} />}
    </Card>
  );
}
