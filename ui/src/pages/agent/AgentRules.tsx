import { useEffect, useState } from 'react';
import { api, enc, errText } from '../../api';
import { useApp } from '../../state';
import type { Agent } from '../../types';
import { CodeEditor, Msg, NoteView, type Note } from '../../components/ui';

/** The agent's rules (read-only when a group manages them) and its local rules. */
export function AgentRules({ a }: { a: Agent }) {
  const app = useApp();
  const managed = !!a.group || !app.can('editor', '');
  const [text, setText] = useState('');
  const [hash, setHash] = useState('');
  const [dirty, setDirty] = useState(false);
  const [note, setNote] = useState<Note>(null);

  const load = async () => {
    try {
      const { data, headers } = await api<string>('GET', `/api/agents/${enc(a.name)}/rules`);
      setText(data); setHash(headers.get('X-Rules-Hash') || ''); setDirty(false);
    } catch (e) { setNote({ kind: 'bad', text: errText(e) }); }
  };
  const version = a.status?.rules_hash;
  useEffect(() => { if (!dirty) load(); }, [a.name, version]); // eslint-disable-line react-hooks/exhaustive-deps

  const validate = async () => {
    try {
      const { data: r } = await api<{ rules: number }>('POST', `/api/agents/${enc(a.name)}/validate`, text, 'application/yaml');
      setNote({ kind: 'ok', text: `Valid: ${r.rules} active rules.` });
    } catch (e) { setNote({ kind: 'bad', text: errText(e) }); }
  };
  const save = async () => {
    try {
      const { data: r } = await api<{ rules: number; unchanged: boolean }>('PUT', `/api/agents/${enc(a.name)}/rules`, text, 'application/yaml');
      setDirty(false);
      setNote({ kind: 'ok', text: r.unchanged ? 'No changes.' : `Applied without restarting: ${r.rules} active rules.` });
      app.refresh();
    } catch (e) { setNote({ kind: 'bad', text: errText(e) }); }
  };

  return (
    <>
      <h3 className="sec" style={{ marginTop: 0 }}>
        {a.group ? <>Rules from group {a.group}</> : 'Rules'} <span className="muted" style={{ fontWeight: 'normal' }}>— what this agent runs</span>
      </h3>
      {managed && a.group && <Msg style={{ margin: '0 0 10px' }}>Group <b>{a.group}</b> manages these rules: they replaced the agent's own rules when it joined the group. To change them, edit the group's rules or move the agent out of the group.</Msg>}
      <CodeEditor value={text} onChange={managed ? undefined : v => { setText(v); setDirty(true); }} />
      <div className="row">
        {managed
          ? a.group && <button onClick={() => app.go({ kind: 'group', name: a.group! })}>Open group {a.group}</button>
          : <>
            <button onClick={validate}>Validate</button>
            <button className="primary" onClick={save}>Save &amp; apply</button>
            <button onClick={() => { setNote(null); load(); }}>Discard changes</button>
          </>}
        <span className="muted mono">{hash && 'version ' + hash}</span>
      </div>
      <NoteView note={note} />
      {a.status?.own_rules_hash && <OwnRules a={a} />}
      <LocalRules a={a} />
    </>
  );
}

/** The agent's own rules, saved when a group replaced them. */
function OwnRules({ a }: { a: Agent }) {
  const app = useApp();
  const [text, setText] = useState<string | null>(null);
  const [note, setNote] = useState<Note>(null);
  const show = async () => {
    try { setText((await api<string>('GET', `/api/agents/${enc(a.name)}/own-rules`)).data); }
    catch (e) { setNote({ kind: 'bad', text: errText(e) }); }
  };
  const restore = async () => {
    if (!confirm(`Make ${a.name} run these rules again, instead of the ones it has now?`)) return;
    try {
      await api('POST', `/api/agents/${enc(a.name)}/own-rules/restore`);
      app.toast(`${a.name} runs its own rules again`);
      app.refresh();
    } catch (e) { setNote({ kind: 'bad', text: errText(e) }); }
  };
  return (
    <>
      <h3 className="sec">Own rules from before a group <span className="muted" style={{ fontWeight: 'normal' }}>— saved, not running</span></h3>
      <p className="muted" style={{ margin: '0 0 8px' }}>
        When a group first replaced this agent's rules, the agent kept a copy (version <code>{a.status!.own_rules_hash}</code>, in <code>rules.before-group.yaml</code>).{' '}
        {a.group ? `Move the agent out of group ${a.group} to restore them.` : 'Restore them to run them again.'}
      </p>
      <div className="row" style={{ marginTop: 0 }}>
        {text === null ? <button onClick={show}>Show them</button> : <button onClick={() => setText(null)}>Hide</button>}
        {!a.group && app.can('editor', '') && <button className="primary" onClick={restore}>Restore these rules</button>}
      </div>
      {text !== null && <pre className="preview mono">{text}</pre>}
      <NoteView note={note} />
    </>
  );
}

const LOCAL_PLACEHOLDER = `(none yet) For example:

rules:
  - name: billing-batch
    files: [/var/log/billing/batch.log]
    pattern: FAILED
    webhooks: [alerts]`;

function LocalRules({ a }: { a: Agent }) {
  const app = useApp();
  const editable = app.can('editor', a.group || '');
  const [text, setText] = useState('');
  const [hash, setHash] = useState('');
  const [enabled, setEnabled] = useState(true);
  const [note, setNote] = useState<Note>(null);

  const load = async () => {
    try {
      const { data: r } = await api<{ rules: string; hash: string; rejected: string; enabled: boolean }>('GET', `/api/agents/${enc(a.name)}/local-rules`);
      setEnabled(r.enabled);
      setText(r.rules || '');
      setHash(r.rules ? r.hash : '');
      setNote(r.rejected ? { kind: 'bad', text: 'Not running: ' + r.rejected } : null);
    } catch (e) { setNote({ kind: 'bad', text: errText(e) }); }
  };
  useEffect(() => { load(); }, [a.name]); // eslint-disable-line react-hooks/exhaustive-deps

  const put = async (body: string) => {
    const clear = body === '';
    if (clear && !confirm(`Remove ${a.name}'s local rules? Only the ${a.group ? "group's " : ''}rules will run.`)) return;
    try {
      const { data: r } = await api<{ rules: number }>('PUT', `/api/agents/${enc(a.name)}/local-rules`, body, 'application/yaml');
      await load();
      setNote({ kind: 'ok', text: clear ? 'Local rules removed.' : `Applied: ${r.rules} local rule${r.rules === 1 ? '' : 's'} on top of the ${a.group ? "group's" : "agent's"} rules.` });
      app.refresh();
    } catch (e) { setNote({ kind: 'bad', text: errText(e) }); }
  };

  return (
    <>
      <h3 className="sec">Local rules <span className="muted" style={{ fontWeight: 'normal' }}>— this machine only, on top of the rules above</span>{' '}
        {enabled && !text.trim() && <span className="badge">none</span>}</h3>
      <p className="muted" style={{ margin: '0 0 8px' }}>
        Extra rules for {a.name} on top of {a.group ? `group ${a.group}'s rules` : 'the rules above'}: for example a file only this server has.
        They can use the {a.group ? "group's " : ''}webhooks, but can only add rules, never replace them. Kept on the agent in <code>rules.local.yaml</code>.
      </p>
      {!enabled ? <Msg>Local rules are turned off on this agent (<code>local_rules_file: off</code>).</Msg> : <>
        <CodeEditor short value={text} onChange={editable ? setText : undefined} placeholder={LOCAL_PLACEHOLDER} />
        {editable && (
          <div className="row">
            <button className="primary" onClick={() => put(text)}>Save local rules</button>
            <button onClick={() => put('')}>Remove local rules</button>
            <span className="muted mono">{hash && 'version ' + hash}</span>
          </div>
        )}
      </>}
      <NoteView note={note} />
    </>
  );
}
