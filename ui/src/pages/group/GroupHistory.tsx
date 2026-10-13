import { useState } from 'react';
import { api, enc, errText, get } from '../../api';
import { useApp, useLoad } from '../../state';
import { pushSummary, rolloutActive } from '../../domain';
import type { Group, GroupState, Version } from '../../types';
import { lineDiff, localTime } from '../../util';
import { DiffView, Empty, Msg } from '../../components/ui';

const SOURCES: Record<string, string> = {
  created: 'created', edited: 'edited', git: 'from git', rollback: 'rollback', 'rollout-abort': 'rollout aborted', existing: 'before history',
};

/** Every version of the group's rules, with diffs and one-click rollback. */
export function GroupHistory({ g }: { g: Group }) {
  const app = useApp();
  const versions = useLoad(() => get<Version[]>(`/api/groups/${enc(g.name)}/versions`), [g.name, g.rules_hash, g.rollout_state?.status]);
  const [shown, setShown] = useState<{ hash: string; text: string; diff: string | null } | null>(null);

  const view = async (hash: string) => {
    try {
      const [text, cur] = await Promise.all([get<string>(`/api/groups/${enc(g.name)}/versions/${enc(hash)}`), get<string>(`/api/groups/${enc(g.name)}/rules`)]);
      setShown({ hash, text, diff: hash === g.rules_hash ? null : lineDiff(cur, text) });
    } catch (e) { app.toast(errText(e), true); }
  };

  const rollback = async (hash: string) => {
    if (!confirm(`Roll back group ${g.name} to version ${hash}? It goes to every agent at once${rolloutActive(g) ? ' and stops the rollout in progress' : ''}${g.source === 'git' ? '. The group stops following git until you sync from git again' : ''}.`)) return;
    try {
      const { data: r } = await api<{ state: GroupState }>('POST', `/api/groups/${enc(g.name)}/rollback`, { hash });
      app.toast(`Rolled back to ${hash}: ` + pushSummary(r.state.results).replace(/\n/g, ' · '));
    } catch (e) { app.toast(errText(e), true); }
    app.refresh();
  };

  if (versions.error) return <div className="pane"><Msg kind="bad">{versions.error}</Msg></div>;
  const list = versions.value;
  if (!list) return <div className="pane muted">Loading…</div>;
  if (!list.length) return <Empty>No versions yet.</Empty>;
  return (
    <div className="pane">
      <p className="muted" style={{ margin: '0 0 10px' }}>
        Every change to this group's rules is kept here (the last 50). Rolling back sends a version to every agent at once
        {g.source === 'git' && ', and the group stops following git until you sync from git again'}.
      </p>
      <div className="scroll"><table>
        <thead><tr><th>Version</th><th>When</th><th>Who</th><th>How</th><th className="num">Lines</th><th></th></tr></thead>
        <tbody>{list.map((v, i) => {
          const current = !!v.current && i === 0;
          return (
            <tr key={i}>
              <td><code>{v.hash}</code> {current && <span className="badge ok">current</span>}{v.stable && <> <span className="badge">previous, still on some agents</span></>}</td>
              <td title={v.time}>{localTime(v.time)}</td>
              <td>{v.author || '—'}</td>
              <td>{SOURCES[v.source] || v.source}{v.note && <div className="muted" style={{ fontSize: 12, whiteSpace: 'normal' }}>{v.note}</div>}</td>
              <td className="num">{v.rules}</td>
              <td>
                <button onClick={() => view(v.hash)}>View</button>{' '}
                {!current && app.can('editor', g.name) && <button onClick={() => rollback(v.hash)}>Roll back to this</button>}
              </td>
            </tr>
          );
        })}</tbody>
      </table></div>
      {shown && <>
        <h3 className="sec">Version {shown.hash}{shown.diff && <> — what rolling back to it would change (<span className="del">−</span> removed, <span className="add">+</span> added)</>}</h3>
        {shown.diff
          ? <><DiffView diff={shown.diff} /><details><summary className="muted">full text</summary><pre className="preview mono">{shown.text}</pre></details></>
          : <pre className="preview mono">{shown.text}</pre>}
      </>}
    </div>
  );
}
