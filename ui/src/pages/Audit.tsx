import { useEffect, useState } from 'react';
import { enc, get } from '../api';
import { useLoad } from '../state';
import type { AuditEntry } from '../types';
import { Badge, Card, DiffView, Empty, Msg } from '../components/ui';

const RESULT: Record<string, 'ok' | 'bad' | 'warn'> = { ok: 'ok', failed: 'bad', denied: 'warn' };

export function AuditPage() {
  const [input, setInput] = useState('');
  const [q, setQ] = useState('');
  useEffect(() => { const t = setTimeout(() => setQ(input), 300); return () => clearTimeout(t); }, [input]);
  const list = useLoad(() => get<AuditEntry[]>(`/api/audit?limit=500&q=${enc(q)}`), [q]);

  return (
    <Card
      title="Audit log"
      sub="Who changed what, when and from where: sign-ins, agents, groups, rules (with diffs), rollouts, silences, settings, tokens, users and access, plus refused attempts."
      actions={<>
        <input type="text" value={input} onChange={e => setInput(e.target.value)} placeholder="Filter: user, action, group…" style={{ width: 240 }} />
        <a className="button" style={{ width: 'auto', padding: '6px 12px' }} href="/api/audit/export">Export (JSON lines)</a>
      </>}
    >
      <div className="pane">
        {list.error ? <Msg kind="bad">{list.error}</Msg> : !list.value ? 'Loading…' : !list.value.length ? <Empty>No entries.</Empty> : (
          <div className="scroll"><table>
            <thead><tr><th>Time</th><th>Who</th><th>Action</th><th>Target</th><th>Result</th><th>Details</th></tr></thead>
            <tbody>{list.value.map((e, i) => (
              <tr key={i}>
                <td title={e.time}>{new Date(e.time).toLocaleString()}</td>
                <td>{e.actor}<div className="muted" style={{ fontSize: 12 }}>{e.method}{e.ip && ' · ' + e.ip}</div></td>
                <td className="mono">{e.action}</td>
                <td>{e.target}{e.group && e.group !== e.target && <span className="muted"> ({e.group})</span>}</td>
                <td><Badge kind={RESULT[e.result] || ''}>{e.result}</Badge></td>
                <td className="wrap">{e.detail}{e.diff && <details><summary className="muted">diff</summary><DiffView diff={e.diff} /></details>}</td>
              </tr>
            ))}</tbody>
          </table></div>
        )}
      </div>
    </Card>
  );
}
