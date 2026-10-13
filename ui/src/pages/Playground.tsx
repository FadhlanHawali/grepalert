import { useEffect, useRef, useState } from 'react';
import { api, enc, errText, get } from '../api';
import { useApp } from '../state';
import { Badge, Card, CodeEditor, Msg } from '../components/ui';

// Shapes of POST /api/playground.
interface SimMatch { rule: string; groups?: Record<string, string>; outcome: 'alert' | 'suppressed' | 'excluded'; alert: number }
interface SimLine { n: number; line: string; matches?: SimMatch[] }
interface SimDelivery { webhook: string; method: string; content_type?: string; body?: string; error?: string }
interface SimAlert { kind: string; rule: string; line: number; at: string; text: string; groups?: Record<string, string>; suppressed?: number; deliveries: SimDelivery[] }
interface Result {
  rules: { name: string; pattern: string; groups?: string[]; absence?: boolean }[];
  result: { lines: SimLine[]; alerts: SimAlert[]; notes?: string[] };
}

const EXAMPLE_RULES = `webhooks:
  - name: chat
    url: \${CHAT_WEBHOOK_URL}
    # The body: text built from what the pattern captured.
    template: '{"text": {{ json (printf "%s, %s is suspended" (title .Severity) .Groups.datasource) }}}'

rules:
  - name: datasource-suspended
    files: [/var/log/app/app.log]
    # (?P<name>...) captures a part of the line as .Groups.name
    pattern: 'Datasource (?P<datasource>\\S+) is suspended because (?P<reason>.+)'
    exclude: 'maintenance'
    severity: critical
    webhooks: [chat]
    suppress: {window: 10m, group_by: datasource}
`;

const EXAMPLE_LINES = `2026-10-14 10:00:01 INFO connection pool healthy
2026-10-14 10:00:02 Datasource DS1 is suspended because the connection pool is exhausted
2026-10-14 10:00:03 Datasource DS1 is suspended because the connection pool is exhausted
2026-10-14 10:00:04 Datasource DS2 is suspended because of the maintenance window
2026-10-14 10:00:05 Datasource DS3 is suspended because its credentials expired
`;

const OUTCOME: Record<SimMatch['outcome'], { kind: 'warn' | ''; text: string; title: string }> = {
  alert: { kind: 'warn', text: 'alert', title: 'an alert would be sent' },
  suppressed: { kind: '', text: 'suppressed', title: 'inside the suppression window: counted in the summary instead' },
  excluded: { kind: '', text: 'excluded', title: 'the exclude pattern matched too' },
};
const KIND: Record<string, string> = { match: 'alert', summary: 'summary when the window closes' };

/** Pretty-prints JSON bodies; leaves anything else as is. */
function pretty(body?: string, ctype?: string) {
  if (!body || !ctype?.includes('json')) return body || '';
  try { return JSON.stringify(JSON.parse(body), null, 2); } catch { return body; }
}

const Captures = ({ groups }: { groups?: Record<string, string> }) =>
  groups && Object.keys(groups).length ? (
    <span className="captures">{Object.entries(groups).map(([k, v]) => <span key={k} className="cap"><b>{k}</b>={v}</span>)}</span>
  ) : null;

export function PlaygroundPage() {
  const app = useApp();
  const [rules, setRules] = useState(EXAMPLE_RULES);
  const [lines, setLines] = useState(EXAMPLE_LINES);
  const [every, setEvery] = useState('1s');
  const [res, setRes] = useState<Result | null>(null);
  const [error, setError] = useState('');
  const [busy, setBusy] = useState(false);
  const seq = useRef(0);

  // Re-run shortly after every change; only the newest answer is shown.
  useEffect(() => {
    const id = ++seq.current;
    const t = setTimeout(async () => {
      setBusy(true);
      try {
        const { data } = await api<Result>('POST', '/api/playground', { rules, lines, every });
        if (id === seq.current) { setRes(data); setError(''); }
      } catch (e) {
        if (id === seq.current) setError(errText(e));
      } finally {
        if (id === seq.current) setBusy(false);
      }
    }, 400);
    return () => clearTimeout(t);
  }, [rules, lines, every]);

  const load = async (from: string) => {
    if (from === 'example') { setRules(EXAMPLE_RULES); setLines(EXAMPLE_LINES); return; }
    try {
      const [kind, name] = [from.slice(0, from.indexOf(':')), from.slice(from.indexOf(':') + 1)];
      setRules(await get<string>(kind === 'group' ? `/api/groups/${enc(name)}/rules` : `/api/agents/${enc(name)}/rules`));
    } catch (e) { app.toast(errText(e), true); }
  };
  const copy = async () => {
    try { await navigator.clipboard.writeText(rules); app.toast('Rules copied'); }
    catch { app.toast('Copy failed: select the text and copy it manually', true); }
  };

  const r = res?.result;
  const matched = r?.lines.filter(l => l.matches?.length).length ?? 0;
  const suppressed = r?.lines.reduce((n, l) => n + (l.matches || []).filter(m => m.outcome === 'suppressed').length, 0) ?? 0;
  const groupsWithRules = app.data.groups.filter(g => g.rules_hash);
  const ownRules = app.data.agents.filter(a => !a.group && a.online);

  return (
    <>
      <Card
        title="Rule playground"
        sub="Try rules on sample log lines: see which lines match, what each rule captures, and exactly what each webhook would receive. Nothing is saved, sent or pushed to agents."
        actions={<>
          <select value="" onChange={e => { load(e.target.value); e.target.value = ''; }} style={{ width: 'auto' }}>
            <option value="">Start from…</option>
            <option value="example">The example</option>
            {groupsWithRules.map(g => <option key={g.name} value={'group:' + g.name}>Rules of group {g.name}</option>)}
            {ownRules.map(a => <option key={a.name} value={'agent:' + a.name}>Rules of agent {a.name}</option>)}
          </select>
          <button onClick={copy}>Copy rules</button>
        </>}
      >
        <div className="pane" style={{ paddingTop: 0 }}>
          <div className="playground">
            <div>
              <h3 className="sec" style={{ marginTop: 0 }}>1. Rules <span className="muted" style={{ fontWeight: 'normal' }}>— pattern, captures and webhook template</span></h3>
              <CodeEditor value={rules} onChange={setRules} />
            </div>
            <div>
              <h3 className="sec" style={{ marginTop: 0 }}>2. Log lines <span className="muted" style={{ fontWeight: 'normal' }}>— paste a sample, one per line</span></h3>
              <CodeEditor value={lines} onChange={setLines} placeholder="Paste log lines here" />
              <div className="row">
                <label className="muted">Lines arrive every{' '}
                  <select value={every} onChange={e => setEvery(e.target.value)} style={{ width: 'auto' }}>
                    <option value="0s">0 s (all at once)</option><option value="1s">1 s</option><option value="10s">10 s</option>
                    <option value="1m">1 min</option><option value="10m">10 min</option>
                  </select>
                </label>
                <span className="muted" style={{ fontSize: 12 }}>Suppression windows use this spacing.</span>
              </div>
            </div>
          </div>
          {error && <Msg kind="bad">{error}</Msg>}
          {res && !error && (
            <div className="muted" style={{ marginTop: 10 }}>
              {busy ? 'Updating… ' : ''}
              <b>{r!.lines.length}</b> lines · <b>{matched}</b> matched · <b>{r!.alerts.length}</b> alert{r!.alerts.length === 1 ? '' : 's'} would be sent
              {suppressed > 0 && <> · <b>{suppressed}</b> suppressed</>}
              {res.rules.map(x => x.groups?.length ? <span key={x.name}> · <code>{x.name}</code> captures {x.groups.map(g => <code key={g}>{g} </code>)}</span> : null)}
            </div>
          )}
          {r?.notes?.map(n => <Msg key={n}>{n}</Msg>)}
        </div>
      </Card>

      {r && !error && <>
        <Card title="3. What matched and what was extracted">
          <div className="scroll"><table>
            <thead><tr><th className="num">#</th><th>Log line</th><th>Matches</th></tr></thead>
            <tbody>
              {r.lines.map(l => (
                <tr key={l.n} className={l.matches?.length ? undefined : 'dim'}>
                  <td className="num muted">{l.n}</td>
                  <td className="wrap mono">{l.line}</td>
                  <td className="wrap">{(l.matches || []).map(m => (
                    <div key={m.rule} className="match">
                      <code>{m.rule}</code> <Badge kind={OUTCOME[m.outcome].kind} title={OUTCOME[m.outcome].title}>
                        {OUTCOME[m.outcome].text}{m.alert >= 0 && ` #${m.alert + 1}`}</Badge> <Captures groups={m.groups} />
                    </div>
                  ))}{!l.matches?.length && <span className="muted">no match</span>}</td>
                </tr>
              ))}
              {!r.lines.length && <tr><td colSpan={3} className="muted">Paste some log lines above.</td></tr>}
            </tbody>
          </table></div>
        </Card>

        <Card title="4. What the webhooks receive">
          <div className="pane" style={{ paddingTop: 0 }}>
            {!r.alerts.length && <div className="muted">No alert would be sent for these lines.</div>}
            {r.alerts.map((a, i) => (
              <div key={i} className="sim-alert">
                <div>
                  <b>#{i + 1}</b> <Badge kind={a.kind === 'match' ? 'warn' : ''}>{KIND[a.kind] || a.kind}</Badge> <code>{a.rule}</code>{' '}
                  <span className="muted">{a.line ? `from line ${a.line}` : `${a.suppressed} more matches held back`} · at {a.at}</span>{' '}
                  <Captures groups={a.groups} />
                </div>
                {a.deliveries.map(d => (
                  <div key={d.webhook} className="delivery">
                    <div className="muted" style={{ fontSize: 12 }}>{d.method} to webhook <b>{d.webhook}</b>{d.content_type && <> · {d.content_type}</>}</div>
                    {d.error ? <Msg kind="bad">{d.error}</Msg> : <pre className="preview mono">{pretty(d.body, d.content_type)}</pre>}
                  </div>
                ))}
                {!a.deliveries.length && <div className="muted">This rule has no webhooks.</div>}
              </div>
            ))}
          </div>
        </Card>
      </>}
    </>
  );
}
