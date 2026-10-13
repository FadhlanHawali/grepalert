import type { DryRunReport } from '../../types';
import { Msg } from '../../components/ui';

function Delta({ now, then }: { now: number; then: number }) {
  if (then > now) return <span className="delta-up">+{then - now}</span>;
  if (then < now) return <span className="delta-down">−{now - then}</span>;
  return <span className="muted">same</span>;
}

/** The result of replaying the agents' recent logs through draft rules. */
export function DryRunView({ r }: { r: DryRunReport }) {
  const rows = r.rules.filter(x => x.current || x.candidate || x.matches);
  const failed = r.agents.filter(a => !a.ok);

  // Notes are per file; the same note from several agents is shown once.
  const notes = new Map<string, string[]>();
  const note = (agent: string, text: string) => notes.set(text, [...(notes.get(text) || []), agent]);
  const samples: Record<string, string[]> = {};
  for (const a of r.agents) {
    const res = a.result;
    if (!a.ok || !res) continue;
    if (res.partial) note(a.agent, 'stopped early (time limit), results are partial');
    for (const f of res.files || []) {
      if (f.estimated) note(a.agent, `${f.path}: no position history yet (the file is new to the agent, or the agent started less than a minute ago), so its last ${Math.round(f.bytes / 1048576) || '<1'} MB were read and line times are estimated`);
      else if (new Date(f.from).getTime() - new Date(res.from).getTime() > 60e3) note(a.agent, `${f.path}: only the last ${Math.round((new Date(res.to).getTime() - new Date(f.from).getTime()) / 60e3)} min are covered (the agent restarted or the file rotated since)`);
      if (f.truncated) note(a.agent, `${f.path}: more than 256 MB in the window; only the newest part was replayed`);
      if (f.error && !/no such file|cannot find/i.test(f.error)) note(a.agent, `${f.path}: ${f.error}`);
    }
    for (const x of res.candidate.rules || []) {
      for (const t of x.samples || []) {
        const list = (samples[x.name] ||= []);
        if (list.length < 3) list.push(`${a.agent}: ${t}`);
      }
    }
  }
  const loud = r.candidate_alerts > r.current_alerts * 3 && r.candidate_alerts > 10;

  return (
    <>
      <h3 className="sec">Dry run: last {r.window} on {r.agents_ok} of {r.agents.length} agents</h3>
      <Msg kind={loud ? 'bad' : 'info'} style={{ margin: '0 0 10px' }}>
        These rules would have sent <b>{r.candidate_alerts}</b> alert{r.candidate_alerts === 1 ? '' : 's'}; the current rules sent <b>{r.current_alerts}</b> for the same lines. Nothing was sent.
      </Msg>
      {rows.length ? (
        <div className="scroll"><table>
          <thead><tr><th>Rule</th><th className="num">Current rules</th><th className="num">These rules</th><th className="num">Change</th><th className="num">Matching lines (all agents)</th><th className="num">Agents alerting</th></tr></thead>
          <tbody>{rows.map(x => (
            <tr key={x.name}>
              <td className="wrap">{x.name}
                {samples[x.name] && <details><summary className="muted">examples</summary><div className="samples mono">{samples[x.name].join('\n')}</div></details>}
              </td>
              <td className="num">{x.current}</td><td className="num">{x.candidate}</td>
              <td className="num"><Delta now={x.current} then={x.candidate} /></td>
              <td className="num">{x.matches}</td><td className="num">{x.agents}</td>
            </tr>
          ))}</tbody>
        </table></div>
      ) : <div className="muted">No rule matched any line in this window.</div>}
      {notes.size > 0 && (
        <ul className="muted" style={{ fontSize: 12.5 }}>
          {[...notes].map(([n, ag]) => (
            <li key={n}>{n} <span title={ag.join(', ')}>({ag.length === r.agents_ok && ag.length > 1 ? 'all agents' : ag.join(', ')})</span></li>
          ))}
        </ul>
      )}
      {failed.length > 0 && <Msg kind="bad">Not run on: {failed.map(a => `${a.agent} (${a.error})`).join(', ')}</Msg>}
    </>
  );
}
