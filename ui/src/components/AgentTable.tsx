import { useApp } from '../state';
import { syncState } from '../domain';
import type { Agent, Labels } from '../types';
import { ago, localTime, mb, uptime, zero } from '../util';
import { Badge, Dot, Empty, cx } from './ui';

export function LabelChips({ labels }: { labels?: Labels }) {
  const entries = Object.entries(labels || {}).filter(([k]) => k !== 'os');
  if (!entries.length) return null;
  return <>{entries.map(([k, v]) => <span key={k}><Badge>{k}={v}</Badge> </span>)}</>;
}

function NameCell({ a }: { a: Agent }) {
  return (
    <>
      <Dot kind={a.revoked ? '' : a.online ? 'ok' : 'bad'} /> {a.name}
      {a.mode === 'connected' && a.group && !a.assigned && <> <Badge title="Joined by label">auto</Badge></>}
      {!zero(a.down_since) && <> <Badge kind="bad" title={`agent-down alert sent; down since ${localTime(a.down_since!)}`}>down</Badge></>}
      <div className="muted mono">
        {a.mode === 'connected' ? <span title="Connects out to the dashboard: no inbound port">⇡ {a.hostname || 'enrolled agent'}</span> : a.url}
      </div>
      {a.labels && <div style={{ marginTop: 3 }}><LabelChips labels={a.labels} /></div>}
    </>
  );
}

export function OfflineText({ a }: { a: Agent }) {
  if (a.revoked) return <><Badge kind="bad">revoked</Badge> <span className="muted">The dashboard refuses this agent until you allow it again (Settings).</span></>;
  if (a.mode === 'connected') return <><Badge kind="bad">offline</Badge> <span className="muted">{zero(a.last_seen) ? 'has not connected since the dashboard started' : 'last seen ' + ago(a.last_seen)}</span></>;
  return <><Badge kind="bad">offline</Badge> <span className="muted">{a.error}</span></>;
}

export function AgentTable({ list, showGroup, group }: { list: Agent[]; showGroup?: boolean; group?: string }) {
  const app = useApp();
  if (!list.length) {
    return (
      <Empty>
        {group ? 'No agents in this group yet.' : 'No agents here.'}
        {app.isAdmin && (
          <div className="row" style={{ justifyContent: 'center' }}>
            <button className="primary" onClick={() => app.openDialog({ kind: 'agent', group })}>Connect agent</button>
            {group && app.data.agents.some(a => a.group !== group) && <button onClick={() => app.openDialog({ kind: 'add-existing', group })}>Add an existing agent</button>}
          </div>
        )}
      </Empty>
    );
  }
  const select = (a: Agent) => {
    app.selectAgent(a.name);
    requestAnimationFrame(() => document.getElementById('agent-detail')?.scrollIntoView({ behavior: 'smooth', block: 'nearest' }));
  };
  return (
    <div className="scroll">
      <table>
        <thead><tr>
          <th>Agent</th>{showGroup && <th>Group</th>}<th>Rules</th><th>Host</th><th>Version</th><th>Uptime</th>
          <th className="num">Files</th><th className="num">Alerts</th><th className="num">Failed / dropped</th><th className="num">Memory</th>
        </tr></thead>
        <tbody>
          {list.map(a => {
            const groupCell = showGroup && (
              <td>{a.group
                ? <a className="badge" style={{ cursor: 'pointer' }} onClick={e => { e.stopPropagation(); app.go({ kind: 'group', name: a.group! }); }}>{a.group}</a>
                : <span className="muted">—</span>}</td>
            );
            const cls = cx('click', app.selAgent === a.name && 'sel');
            if (!a.online || !a.status) {
              return (
                <tr key={a.name} className={cls} onClick={() => select(a)}>
                  <td><NameCell a={a} /></td>{groupCell}
                  <td colSpan={8} className="wrap"><OfflineText a={a} /></td>
                </tr>
              );
            }
            const s = a.status, st = syncState(a, app.groupOf(a.group));
            const bad = s.webhooks.failed + s.webhooks.dropped;
            return (
              <tr key={a.name} className={cls} onClick={() => select(a)}>
                <td><NameCell a={a} /></td>{groupCell}
                <td>{st ? <Badge kind={st.cls} title={st.title}>{st.text}</Badge>
                  : <><code>{s.rules_hash}</code> <span className="muted">{(s.rules || []).length} rules</span></>}</td>
                <td>{s.host}</td><td>{s.version}</td><td>{uptime(s.started_at)}</td>
                <td className="num">{(s.files || []).length}</td>
                <td className="num" title={`sent by the agent: ${s.webhooks.sent}, handed to the dashboard: ${s.webhooks.via_dashboard || 0}`}>{s.webhooks.sent + (s.webhooks.via_dashboard || 0)}</td>
                <td className="num">{bad ? <Badge kind="bad">{s.webhooks.failed} / {s.webhooks.dropped}</Badge> : '0 / 0'}</td>
                <td className="num">{mb(s.memory.sys)}</td>
              </tr>
            );
          })}
        </tbody>
      </table>
    </div>
  );
}
