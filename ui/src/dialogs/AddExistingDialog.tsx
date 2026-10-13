import { useState } from 'react';
import { api, enc, errText } from '../api';
import { useApp } from '../state';
import { Modal } from '../components/ui';

/** Move existing agents into a group. */
export function AddExistingDialog({ group }: { group: string }) {
  const app = useApp();
  const candidates = app.data.agents.filter(a => a.group !== group);
  const [picked, setPicked] = useState<Set<string>>(new Set());
  const toggle = (n: string) => setPicked(p => { const s = new Set(p); if (s.has(n)) s.delete(n); else s.add(n); return s; });

  const add = async () => {
    app.closeDialog();
    for (const n of picked) {
      const a = app.agentOf(n)!;
      try {
        const { data: r } = await api<{ push?: { ok: boolean; action: string; error?: string } }>('PUT', `/api/agents/${enc(n)}`,
          { url: a.url, group, insecure_skip_verify: a.insecure_skip_verify });
        app.toast(`${n} added to ${group}` + (r.push ? (r.push.ok ? ` · rules ${r.push.action}` : ` · push failed: ${r.push.error}`) : ''), !!r.push && !r.push.ok);
      } catch (e) { app.toast(`${n}: ${errText(e)}`, true); }
    }
    app.refresh();
  };

  return (
    <Modal onClose={app.closeDialog}>
      <form method="dialog" onSubmit={e => { e.preventDefault(); add(); }}>
        <h2>Add agents to {group}</h2>
        <p className="muted" style={{ margin: 0 }}>The group's rules replace each agent's current rules.</p>
        <div style={{ display: 'grid', gap: 6, maxHeight: 300, overflow: 'auto' }}>
          {candidates.map(a => (
            <label key={a.name} className="check">
              <input type="checkbox" checked={picked.has(a.name)} onChange={() => toggle(a.name)} />
              <b>{a.name}</b> <span className="muted">{a.group ? 'in ' + a.group : 'ungrouped'} · {a.url || a.hostname || ''}</span>
            </label>
          ))}
        </div>
        <div className="foot">
          <button type="button" onClick={app.closeDialog}>Cancel</button>
          <button type="submit" className="primary" disabled={!picked.size}>Add to group</button>
        </div>
      </form>
    </Modal>
  );
}
