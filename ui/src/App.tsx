import { useApp, logout, type View } from './state';
import { activeSilences, groupHealth } from './domain';
import { Dot, Empty } from './components/ui';
import { ErrorBoundary } from './components/ErrorBoundary';
import { AgentDialog } from './dialogs/AgentDialog';
import { AddExistingDialog } from './dialogs/AddExistingDialog';
import { GroupDialog } from './dialogs/GroupDialog';
import { AccessPage } from './pages/Access';
import { AgentsPage, Welcome } from './pages/AgentsPage';
import { AlertsPage } from './pages/Alerts';
import { AuditPage } from './pages/Audit';
import { EnrollPage } from './pages/Enroll';
import { SettingsPage } from './pages/Settings';
import { SilencesPage } from './pages/Silences';
import { PlaygroundPage } from './pages/Playground';
import { AgentDetail } from './pages/agent/AgentDetail';
import { GroupPage } from './pages/group/GroupPage';

export function App() {
  const app = useApp();
  const { me, isAdmin } = app;
  const p = me.perms;
  const noAccess = !me.open && !p.admin && !p.global && !Object.keys(p.groups || {}).length;
  const online = app.data.agents.filter(a => a.online).length;
  const roleText = p.admin ? 'admin' : [p.global ? `${p.global} (all groups)` : '',
    ...Object.entries(p.groups || {}).map(([g, r]) => `${r} of ${g}`)].filter(Boolean).join(', ') || 'no access';

  return (
    <>
      <header>
        <h1><span>grep</span>alert</h1>
        <span className="muted hide-sm">
          {app.error ? 'dashboard error: ' + app.error : app.data.agents.length ? `${online}/${app.data.agents.length} agents online` : ''}
        </span>
        <span className="spacer" />
        <label className="muted hide-sm"><input type="checkbox" checked={app.auto} onChange={e => app.setAuto(e.target.checked)} /> auto-refresh</label>
        {isAdmin && <>
          <button onClick={() => app.openDialog({ kind: 'group' })}>New group</button>
          <button className="primary" onClick={() => app.openDialog({ kind: 'agent' })}>Connect agent</button>
        </>}
        {!me.open && (
          <div className="usermenu">
            <div className="who"><b>{me.identity.name || me.identity.user}</b><small>{roleText}</small></div>
            <button onClick={logout} title="Sign out">Sign out</button>
          </div>
        )}
      </header>
      {me.open && (
        <div className="banner">⚠ No sign-in is configured: anyone who can reach this dashboard is an admin. Set <code>auth.password</code>, local users or <code>auth.oidc</code> in the dashboard config.</div>
      )}
      <div className="layout">
        <nav>{!noAccess && <Nav />}</nav>
        <main>
          {noAccess ? (
            <div className="card"><Empty title="No access yet">
              <p>You are signed in as <b>{me.identity.user}</b>, but no role has been granted to you.<br />Ask an admin to give you access under <b>Users &amp; access</b>.</p>
            </Empty></div>
          ) : <ErrorBoundary resetKey={JSON.stringify(app.view)}><Main /></ErrorBoundary>}
        </main>
      </div>
      <Dialogs />
      <div id="toast">{app.toasts.map(t => <div key={t.id} className={t.bad ? 'bad' : undefined}>{t.text}</div>)}</div>
    </>
  );
}

function Nav() {
  const app = useApp();
  const { view, data } = app;
  const on = (v: View) => (view.kind === v.kind && (v.kind !== 'group' || (view.kind === 'group' && view.name === v.name)) ? 'on' : '');
  const link = (v: View, children: React.ReactNode) => (
    <a className={on(v)} href={'#' + (v.kind === 'group' ? 'group/' + encodeURIComponent(v.name) : v.kind)}
      onClick={e => { e.preventDefault(); app.go(v); }}>{children}</a>
  );
  const silenced = activeSilences(app.silences).length;
  return (
    <>
      {link({ kind: 'all' }, <>All agents <span className="count">{data.agents.length}</span></>)}
      {link({ kind: 'ungrouped' }, <>Ungrouped <span className="count">{data.agents.filter(a => !a.group).length}</span></>)}
      <div className="label">Groups</div>
      {data.groups.map(g => (
        <span key={g.name} style={{ display: 'contents' }}>
          {link({ kind: 'group', name: g.name }, <><Dot kind={groupHealth(g, n => app.agentOf(n))} />{g.name}<span className="count">{g.members.length}</span></>)}
        </span>
      ))}
      {app.isAdmin && <a className="new" onClick={() => app.openDialog({ kind: 'group' })}>+ New group</a>}
      <div className="label">Alerts</div>
      {link({ kind: 'alerts' }, 'Alert history')}
      {link({ kind: 'silences' }, <>Silences {silenced > 0 && <span className="count">{silenced} active</span>}</>)}
      <div className="label">Tools</div>
      {link({ kind: 'playground' }, 'Rule playground')}
      {app.isAdmin && <>
        <div className="label">Setup</div>
        {link({ kind: 'enroll' }, 'Agent enrollment')}
        {link({ kind: 'access' }, <>Users &amp; access</>)}
        {link({ kind: 'audit' }, 'Audit log')}
        {link({ kind: 'settings' }, 'Settings')}
      </>}
    </>
  );
}

function Main() {
  const app = useApp();
  const { view, data } = app;
  switch (view.kind) {
    case 'enroll': return <EnrollPage />;
    case 'alerts': return <AlertsPage />;
    case 'silences': return <SilencesPage />;
    case 'settings': return <SettingsPage />;
    case 'access': return <AccessPage />;
    case 'audit': return <AuditPage />;
    case 'playground': return <PlaygroundPage />;
  }
  if (!app.loaded) return <div className="muted">Loading…</div>;
  if (!data.agents.length && !data.groups.length) return <Welcome />;
  const sel = app.agentOf(app.selAgent || undefined);
  if (view.kind === 'group') {
    const g = app.groupOf(view.name);
    if (!g) return <Empty title="No such group">It may have been deleted. <button className="link" onClick={() => app.go({ kind: 'all' })}>Show all agents</button></Empty>;
    return <><GroupPage key={g.name} g={g} />{sel && <AgentDetail a={sel} />}</>;
  }
  return <><AgentsPage ungrouped={view.kind === 'ungrouped'} />{sel && <AgentDetail a={sel} />}</>;
}

function Dialogs() {
  const { dialog: d } = useApp();
  if (!d) return null;
  if (d.kind === 'agent') return <AgentDialog agent={d.agent} group={d.group} />;
  if (d.kind === 'group') return <GroupDialog group={d.group} />;
  return <AddExistingDialog group={d.group} />;
}
