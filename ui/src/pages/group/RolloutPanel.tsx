import { Fragment } from 'react';
import { api, enc, errText } from '../../api';
import { useApp } from '../../state';
import { stageText, stagesOf } from '../../domain';
import type { Group } from '../../types';
import { ago, durMs } from '../../util';

const TITLES = { running: 'Rolling out', halted: 'Rollout stopped', done: 'Rolled out', aborted: 'Rollout aborted' };

const ACTIONS = {
  promote: { ask: () => 'Move the rollout to its next stage now?', done: 'Moved to the next stage' },
  promote_all: { ask: (h: string) => `Send ${h} to every agent now?`, done: 'Sent to every agent' },
  retry: { ask: () => 'Start the rollout again from the first stage, with other agents first?', done: 'Rollout restarted' },
  abort: { ask: (_: string, stable: string, git: boolean) => `Abort the rollout? The group's rules go back to ${stable} on every agent${git ? ', and the group stops following git until you sync from git again' : ''}.`, done: 'Rollout aborted' },
};
type Action = keyof typeof ACTIONS;

/** Progress and controls of a group's staged rollout. */
export function RolloutPanel({ g }: { g: Group }) {
  const app = useApp();
  const ro = g.rollout_state;
  if (!ro) return null;
  const live = ro.status === 'running' || ro.status === 'halted';
  if (!live && Date.now() - new Date(ro.updated_at).getTime() > 30 * 60e3) return null; // the History tab has it

  const act = async (op: Action) => {
    if (!confirm(ACTIONS[op].ask(ro.hash, ro.stable_hash, g.source === 'git'))) return;
    try { await api('POST', `/api/groups/${enc(g.name)}/rollout`, { action: op }); app.toast(ACTIONS[op].done); }
    catch (e) { app.toast(errText(e), true); }
    app.refresh();
  };

  const stages = stagesOf(g);
  let detail: React.ReactNode = ro.reason;
  if (ro.status === 'running') {
    const left = new Date(ro.stage_started).getTime() + durMs(g.rollout.soak) - Date.now();
    const canaries = g.canaries || [];
    detail = <>
      {canaries.length} of {g.members.length} agents run <code>{ro.hash}</code> ({canaries.join(', ')}), the rest stay on <code>{ro.stable_hash}</code>.{' '}
      {left > 90e3 ? `Next check in about ${Math.ceil(left / 60e3)} min.` : left > 0 ? `Next check in about ${Math.ceil(left / 1e3)} s.` : 'Being checked now.'}
    </>;
  } else if (ro.status === 'halted') {
    detail = <>Every agent is back on <code>{ro.stable_hash}</code>. {ro.reason}</>;
  }
  const editor = app.can('editor', g.name);

  return (
    <div className="pane" style={{ paddingTop: 0 }}>
      <div className={`rollout ${ro.status}`}>
        <div>
          <b>{TITLES[ro.status]}</b> <code>{ro.stable_hash}</code> → <code>{ro.hash}</code>{' '}
          <span className="muted">started {ago(ro.started_at)}{ro.started_by ? ' by ' + ro.started_by : ''}</span>
        </div>
        <div className="steps">
          {stages.map((st, i) => {
            const cls = ro.status === 'done' || i < ro.stage ? 'done' : i === ro.stage && ro.status === 'running' ? 'on' : '';
            return <Fragment key={i}>{i > 0 && <span className="arrow">→</span>}<span className={`step ${cls}`}>{stageText(st)}</span></Fragment>;
          })}
        </div>
        <div>{detail}</div>
        {editor && live && (
          <div className="row">
            {ro.status === 'running' ? <>
              <button onClick={() => act('promote')}>Next stage now</button>
              <button onClick={() => act('promote_all')}>Send to all agents now</button>
            </> : <button onClick={() => act('retry')}>Try again</button>}
            <button className="danger" onClick={() => act('abort')}>Abort and go back to {ro.stable_hash}</button>
          </div>
        )}
        {!!ro.log?.length && (
          <details>
            <summary className="muted">Rollout log</summary>
            <ul>{ro.log.slice().reverse().map((n, i) => <li key={i}>{new Date(n.time).toLocaleTimeString()} — {n.message}</li>)}</ul>
          </details>
        )}
      </div>
    </div>
  );
}
