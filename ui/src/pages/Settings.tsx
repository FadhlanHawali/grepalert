import { useEffect, useState } from 'react';
import { api, errText, get } from '../api';
import { Card, Field, Msg, NoteView, type Note } from '../components/ui';

interface Settings { ops_webhook?: { url?: string; template?: string }; agent_down_after?: string }

export function SettingsPage() {
  const [loaded, setLoaded] = useState(false);
  const [error, setError] = useState('');
  const [url, setUrl] = useState('');
  const [tmpl, setTmpl] = useState('');
  const [after, setAfter] = useState('2m');
  const [note, setNote] = useState<Note>(null);

  useEffect(() => {
    get<Settings>('/api/settings').then(st => {
      setUrl(st.ops_webhook?.url || ''); setTmpl(st.ops_webhook?.template || ''); setAfter(st.agent_down_after || '2m');
      setLoaded(true);
    }).catch(e => setError(errText(e)));
  }, []);

  const save = async () => {
    try {
      await api('PUT', '/api/settings', { ops_webhook: { url: url.trim(), template: tmpl.trim() }, agent_down_after: after });
      setNote({ kind: 'ok', text: 'Saved.' });
    } catch (e) { setNote({ kind: 'bad', text: errText(e) }); }
  };
  const test = async () => {
    try {
      const { data: r } = await api<{ sent: string }>('POST', '/api/settings/test-ops');
      setNote({ kind: 'ok', text: `Test alert sent to ${r.sent}.` });
    } catch (e) { setNote({ kind: 'bad', text: `Test failed: ${errText(e)} (save the settings first)` }); }
  };

  return (
    <Card title="Settings">
      <div className="pane" style={{ paddingTop: 0 }}>
        {error ? <Msg kind="bad">{error}</Msg> : !loaded ? 'Loading…' : (
          <div className="stack" style={{ maxWidth: 720 }}>
            <h3 className="sec" style={{ marginTop: 0 }}>Agent-down alerts</h3>
            <p className="muted" style={{ margin: 0 }}>When an agent stops reporting, the dashboard alerts here (a group can use its own URL instead). It alerts again when the agent is back. Stopped rule rollouts are reported here too.</p>
            <Field label="Ops webhook URL">
              <input type="text" value={url} onChange={e => setUrl(e.target.value)} placeholder="https://hooks.slack.com/services/…  or  ${OPS_WEBHOOK_URL}" />
            </Field>
            <Field label={<>Body template <span className="muted">(optional)</span></>} hint="Go template with the alert fields; empty sends the alert as JSON.">
              <input type="text" value={tmpl} onChange={e => setTmpl(e.target.value)} placeholder='{"text": {{ json .Text }}}' />
            </Field>
            <Field label="Alert when unreachable for" style={{ maxWidth: 260 }}>
              <select value={after} onChange={e => setAfter(e.target.value)}>
                {['30s', '1m', '2m', '5m', '10m', '30m'].map(v => <option key={v}>{v}</option>)}
              </select>
            </Field>
            <div className="row" style={{ margin: 0 }}>
              <button className="primary" onClick={save}>Save</button>
              <button onClick={test}>Send a test alert</button>
            </div>
            <NoteView note={note} />
          </div>
        )}
      </div>
    </Card>
  );
}
