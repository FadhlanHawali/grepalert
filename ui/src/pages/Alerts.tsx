import { useState } from 'react';
import { enc, get } from '../api';
import { useApp, useLoad } from '../state';
import type { HistoryEntry } from '../types';
import { Badge, Card, Empty, Msg } from '../components/ui';

const STATUS: Record<string, 'ok' | 'bad' | 'warn' | ''> = { sent: 'ok', silenced: '', failed: 'bad', dropped: 'bad', 'no destination': 'warn' };
const KINDS: Record<string, string> = {
  match: 'match', summary: 'agent summary', fleet_summary: 'held back', absent: 'missing line', recovered: 'recovered',
  agent_down: 'agent down', agent_up: 'agent back', rollout: 'rollout stopped',
};

export function AlertsPage() {
  const app = useApp();
  const [group, setGroup] = useState('');
  const list = useLoad(() => get<HistoryEntry[]>(`/api/alerts?limit=300&group=${enc(group)}`), [group, app.data]);
  return (
    <Card
      title="Alert history"
      sub="Alerts the dashboard handled for agents that connect to it: sent (combined across hosts), silenced, failed, and agent-down/up. Alerts an agent sent itself (direct agents, or while the dashboard was unreachable) are in that agent's Events tab."
      actions={<select value={group} onChange={e => setGroup(e.target.value)}>
        <option value="">All groups</option>
        {app.data.groups.map(g => <option key={g.name}>{g.name}</option>)}
      </select>}
    >
      <div className="pane">
        {list.error ? <Msg kind="bad">{list.error}</Msg> : !list.value ? <div className="muted">Loading…</div> : !list.value.length ? <Empty>No alerts yet.</Empty> : (
          <div className="scroll"><table>
            <thead><tr><th>Time</th><th>Status</th><th>Kind</th><th>Group</th><th>Rule</th><th className="num">Hosts</th><th>Message</th></tr></thead>
            <tbody>{list.value.map(e => (
              <tr key={e.id + e.time}>
                <td title={e.time}>{new Date(e.time).toLocaleTimeString()}</td>
                <td><Badge kind={STATUS[e.status] ?? ''} title={e.detail}>{e.status}</Badge></td>
                <td>{KINDS[e.kind] || e.kind}</td><td>{e.group || '—'}</td>
                <td>{e.rule}{e.group_key && <span className="muted"> ({e.group_key})</span>}</td>
                <td className="num" title={(e.hosts || []).join(', ')}>{e.host_count}</td>
                <td className="wrap mono">{e.text}</td>
              </tr>
            ))}</tbody>
          </table></div>
        )}
      </div>
    </Card>
  );
}
