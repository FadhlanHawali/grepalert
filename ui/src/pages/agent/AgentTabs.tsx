import { useState } from 'react';
import { api, enc, errText, get } from '../../api';
import { useLoad } from '../../state';
import type { Agent } from '../../types';
import { ago, mb } from '../../util';
import { Badge, Empty, Msg, Stat, type Note, NoteView } from '../../components/ui';

export function AgentOverview({ a }: { a: Agent }) {
  if (!a.online || !a.status) {
    return <><Msg kind="bad">Cannot reach this agent: {a.error}</Msg><p className="muted">Check the URL and token under <b>Settings</b>.</p></>;
  }
  const s = a.status;
  const limit = s.memory.memory_limit > 0 && s.memory.memory_limit < 9e18 ? mb(s.memory.memory_limit) : 'none';
  return (
    <>
      <div className="grid">
        <Stat k="Heap in use" v={mb(s.memory.heap_inuse)} />
        <Stat k="Memory from OS" v={mb(s.memory.sys)} />
        <Stat k="Memory limit" v={limit} />
        {s.dashboard && <><Stat k="Alerts via dashboard" v={s.webhooks.via_dashboard} /><Stat k="Sent directly (fallback)" v={s.webhooks.fallback} /></>}
        <Stat k="Alerts sent" v={s.webhooks.sent} />
        <Stat k="Webhook queue" v={s.webhooks.queued} />
        <Stat k="Suppression keys" v={s.suppress_keys} />
      </div>
      <div className="muted">
        Rules <code>{s.rules_hash}</code> loaded {ago(s.rules_loaded_at)} from <b>{s.rules_origin || '—'}</b>
        {s.local_rules_hash && (s.local_rules_error
          ? <> · <Badge kind="bad" title={s.local_rules_error}>local rules rejected</Badge></>
          : <> · plus {s.local_rules} local rule{s.local_rules === 1 ? '' : 's'} <code>{s.local_rules_hash}</code></>)}
      </div>
      <h3 className="sec">Rules</h3>
      <div className="scroll"><table>
        <thead><tr><th>Rule</th><th>Pattern</th><th>Suppression</th><th className="num">Matches</th><th className="num">Alerts</th><th className="num">Suppressed</th><th>Last match</th></tr></thead>
        <tbody>
          {(s.rules || []).map(r => (
            <tr key={r.name}>
              <td>{r.name}{r.local && <> <Badge title="from this machine's local rules">local</Badge></>}</td>
              <td className="wrap">{r.absent
                ? <><Badge kind={r.firing ? 'bad' : 'ok'}>{r.firing ? 'missing' : 'seen'}</Badge> absent <code>{r.absent}</code> for {r.for}</>
                : <code>{r.pattern}</code>}</td>
              <td>{r.suppress || (r.absent ? 'once, then on recovery' : 'off')}</td>
              <td className="num">{r.matches}</td><td className="num">{r.alerts}</td><td className="num">{r.suppressed}</td><td>{ago(r.last_match)}</td>
            </tr>
          ))}
          {!s.rules?.length && <tr><td colSpan={7} className="muted">No active rules</td></tr>}
        </tbody>
      </table></div>
      {s.kubernetes && <>
        <h3 className="sec">Kubernetes sources</h3>
        {s.kubernetes.error && <Msg kind="bad">{s.kubernetes.error}</Msg>}
        <div className="scroll"><table>
          <thead><tr><th>Namespace</th><th>Pods selected by</th><th>Container</th><th className="num">Pods</th><th>State</th></tr></thead>
          <tbody>{s.kubernetes.sources.map(k => (
            <tr key={k.namespace + k.selector + k.container}>
              <td>{k.namespace === '*' ? 'all namespaces' : k.namespace}</td>
              <td><code>{k.selector || 'every pod'}</code></td>
              <td>{k.container || 'all'}</td>
              <td className="num">{k.pods}</td>
              <td className="wrap">{k.error ? <Badge kind="bad" title={k.error}>error</Badge> : <Badge kind="ok">following</Badge>}{k.error && <span className="muted"> {k.error}</span>}</td>
            </tr>
          ))}</tbody>
        </table></div>
      </>}
      <h3 className="sec">{s.kubernetes ? 'Tailed files and pod logs' : 'Tailed files'}</h3>
      <div className="scroll"><table>
        <thead><tr><th>File</th><th>State</th><th className="num">Offset</th><th className="num">Lines read</th><th>Rules</th></tr></thead>
        <tbody>
          {(s.files || []).map(f => (
            <tr key={f.path}>
              <td className="wrap"><code>{f.path}</code></td>
              <td>{f.open ? <Badge kind="ok">{f.path.startsWith('k8s:') ? 'following' : 'tailing'}</Badge>
                : <Badge kind="warn" title={f.error}>{f.error ? 'error' : f.path.startsWith('k8s:') ? 'connecting' : 'waiting for file'}</Badge>}</td>
              <td className="num">{f.offset}</td><td className="num">{f.lines}</td><td className="wrap">{(f.rules || []).join(', ')}</td>
            </tr>
          ))}
          {!s.files?.length && <tr><td colSpan={5} className="muted">No files</td></tr>}
        </tbody>
      </table></div>
    </>
  );
}

const EVENT_KIND: Record<string, 'ok' | 'warn' | 'bad'> = { alert: 'warn', webhook_error: 'bad', error: 'bad', rules: 'ok' };

export function AgentEvents({ a }: { a: Agent }) {
  const ev = useLoad(() => get<{ time: string; type: string; rule?: string; message: string }[]>(`/api/agents/${enc(a.name)}/events?limit=200`), [a.name]);
  if (ev.error) return <Msg kind="bad">{ev.error}</Msg>;
  if (!ev.value) return <div className="muted">Loading…</div>;
  if (!ev.value.length) return <Empty>No events yet.</Empty>;
  return (
    <div className="scroll"><table>
      <thead><tr><th>Time</th><th>Type</th><th>Rule</th><th>Message</th></tr></thead>
      <tbody>{ev.value.map((e, i) => (
        <tr key={i}>
          <td title={e.time}>{new Date(e.time).toLocaleString()}</td>
          <td><Badge kind={EVENT_KIND[e.type] || ''}>{e.type}</Badge></td>
          <td>{e.rule}</td><td className="wrap mono">{e.message}</td>
        </tr>
      ))}</tbody>
    </table></div>
  );
}

export function AgentTest({ a }: { a: Agent }) {
  const [line, setLine] = useState('');
  const [note, setNote] = useState<Note>(null);
  const run = async () => {
    try {
      const { data: r } = await api<{ matched?: string[] }>('POST', `/api/agents/${enc(a.name)}/test`, { line });
      setNote(r.matched?.length ? { kind: 'ok', text: 'Matches: ' + r.matched.join(', ') } : { kind: 'info', text: 'No rule matches this line.' });
    } catch (e) { setNote({ kind: 'bad', text: errText(e) }); }
  };
  return (
    <>
      <input type="text" value={line} onChange={e => setLine(e.target.value)} onKeyDown={e => e.key === 'Enter' && run()}
        placeholder="Paste a log line to see which of this agent's rules match it" />
      <div className="row"><button className="primary" onClick={run}>Test</button></div>
      <NoteView note={note} />
    </>
  );
}
