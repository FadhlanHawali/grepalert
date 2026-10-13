import { api, enc, errText } from '../api';
import { useApp } from '../state';
import { activeSilences, scopeText } from '../domain';
import { until } from '../util';
import { Msg } from './ui';

/** Active silences that cover a group. */
export function SilenceBanner({ group }: { group: string }) {
  const app = useApp();
  const act = activeSilences(app.silences).filter(s => !s.group || s.group === group);
  if (!act.length) return null;
  const end = async (id: string) => {
    try { await api('DELETE', `/api/silences/${enc(id)}`); app.toast('Silence ended'); app.refresh(); }
    catch (e) { app.toast(errText(e), true); }
  };
  return (
    <div className="pane" style={{ paddingTop: 0 }}>
      {act.map(s => (
        <Msg key={s.id} style={{ margin: '0 0 6px' }}>
          🔕 <b>Silenced</b> ({scopeText(s)}) for another {until(s.ends_at)}{s.reason ? ' — ' + s.reason : ''}{' '}
          <button className="link" onClick={() => end(s.id)}>End now</button>
        </Msg>
      ))}
    </div>
  );
}
