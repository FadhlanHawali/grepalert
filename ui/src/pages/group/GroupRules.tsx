import { useEffect, useState } from 'react';
import { api, enc, errText } from '../../api';
import { useApp } from '../../state';
import { pushSummary, stageText, stagesOf } from '../../domain';
import type { DryRunReport, Group, GroupState, Rollout } from '../../types';
import { CodeEditor, Msg, NoteView, type Note } from '../../components/ui';
import { DryRunView } from './DryRunView';

/** The group's rules: edit, validate, save, dry-run, or preview git. */
export function GroupRules({ g }: { g: Group }) {
  const app = useApp();
  const [text, setText] = useState('');
  const [hash, setHash] = useState('');
  const [dirty, setDirty] = useState(false);
  const [note, setNote] = useState<Note>(null);
  const [dry, setDry] = useState<DryRunReport | null>(null);
  const [dryBusy, setDryBusy] = useState(false);
  const [window_, setWindow] = useState('1h');
  const [preview, setPreview] = useState<{ text: string; hash: string } | null>(null);

  const editable = g.source === 'manual' && app.can('editor', g.name);
  const members = g.members.length;

  const load = async () => {
    try {
      const { data, headers } = await api<string>('GET', `/api/groups/${enc(g.name)}/rules`);
      setText(data || '');
      setHash(headers.get('X-Rules-Hash') || '');
      setDirty(false);
    } catch (e) { setNote({ kind: 'bad', text: errText(e) }); }
  };
  // Reload when the group's version changes, unless the user is editing.
  useEffect(() => { if (!dirty) load(); }, [g.name, g.rules_hash]); // eslint-disable-line react-hooks/exhaustive-deps

  const validate = async () => {
    try {
      const { data: r } = await api<{ rules: number }>('POST', `/api/groups/${enc(g.name)}/validate`, text, 'application/yaml');
      setNote({ kind: 'ok', text: `Valid: ${r.rules} active rules.` });
    } catch (e) { setNote({ kind: 'bad', text: errText(e) }); }
  };

  const save = async () => {
    try {
      const { data: r } = await api<{ hash: string; rules: number; state: GroupState; rollout?: Rollout }>('PUT', `/api/groups/${enc(g.name)}/rules`, text, 'application/yaml');
      setDirty(false); setDry(null); setPreview(null);
      const ro = r.rollout;
      setNote({
        kind: 'ok',
        text: ro && ro.status === 'running' && ro.hash === r.hash
          ? `Saved version ${r.hash} (${r.rules} active rules). Staged rollout started: ${stageText(stagesOf(g)[0])} first; the others stay on ${ro.stable_hash} for now.\n${pushSummary(r.state.results)}`
          : `Saved version ${r.hash} (${r.rules} active rules), applied without restarting:\n${pushSummary(r.state.results)}`,
      });
      app.refresh();
    } catch (e) { setNote({ kind: 'bad', text: errText(e) }); }
  };

  const showGit = async () => {
    try {
      const { data, headers } = await api<string>('GET', `/api/groups/${enc(g.name)}/preview`);
      setPreview({ text: data, hash: headers.get('X-Rules-Hash') || '' });
    } catch (e) { setNote({ kind: 'bad', text: errText(e) }); }
  };

  const dryRun = async () => {
    setDryBusy(true); setDry(null);
    setNote({ kind: 'info', text: "Replaying the agents' logs…" });
    try {
      const rules = g.source === 'git' ? (await api<string>('GET', `/api/groups/${enc(g.name)}/preview`)).data : text;
      const { data } = await api<DryRunReport>('POST', `/api/groups/${enc(g.name)}/dryrun?window=${enc(window_)}`, rules, 'application/yaml');
      setDry(data); setNote(null);
    } catch (e) { setNote({ kind: 'bad', text: errText(e) }); }
    finally { setDryBusy(false); }
  };

  const staged = g.rollout?.enabled && !!g.rules_hash;
  return (
    <div className="pane">
      {g.source === 'git' && <Msg style={{ margin: '0 0 10px' }}>These rules come from git and are read-only here. Change the file in the repository, then click <b>Sync from git</b>.</Msg>}
      <CodeEditor value={text} onChange={editable ? v => { setText(v); setDirty(true); } : undefined} placeholder={g.source === 'git' ? 'Not synced yet' : ''} />
      <div className="row">
        {editable ? <>
          <button onClick={validate}>Validate</button>
          <button className="primary" onClick={save}>{staged ? 'Save & start staged rollout' : `Save & push to ${members} agent${members === 1 ? '' : 's'}`}</button>
          <button onClick={() => { setNote(null); load(); }}>Discard changes</button>
        </> : <button onClick={showGit}>Show latest from git</button>}
        {app.can('editor', g.name) && members > 0 && <>
          <span className="spacer" />
          <select style={{ width: 'auto' }} value={window_} onChange={e => setWindow(e.target.value)}>
            <option value="15m">last 15 minutes</option><option value="1h">last hour</option><option value="3h">last 3 hours</option>
          </select>
          <button disabled={dryBusy} onClick={dryRun} title={`Replay the agents' recent logs through ${g.source === 'git' ? 'the latest rules in git' : 'the rules in the editor'}; nothing is sent`}>Dry run</button>
        </>}
        <span className="muted mono">{hash && 'version ' + hash}</span>
      </div>
      <NoteView note={note} />
      {dry && <DryRunView r={dry} />}
      {preview && <>
        <h3 className="sec">Latest in git (version {preview.hash}{preview.hash === g.rules_hash ? ', same as applied' : <>, <b>not applied yet</b></>})</h3>
        <pre className="preview mono">{preview.text}</pre>
      </>}
    </div>
  );
}
