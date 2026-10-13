import { useApp } from '../state';
import { AgentTable } from '../components/AgentTable';
import { Card, Empty } from '../components/ui';

/** All agents, or only the ungrouped ones. */
export function AgentsPage({ ungrouped }: { ungrouped?: boolean }) {
  const app = useApp();
  const list = ungrouped ? app.data.agents.filter(a => !a.group) : app.data.agents;
  return (
    <Card
      title={ungrouped ? 'Ungrouped agents' : 'All agents'}
      sub={ungrouped ? 'Each of these agents keeps its own rules. Add them to a group to manage rules together.' : 'Every agent connected to this dashboard.'}
      actions={app.isAdmin && <button className="primary" onClick={() => app.openDialog({ kind: 'agent' })}>Connect agent</button>}
    >
      <AgentTable list={list} showGroup={!ungrouped} />
    </Card>
  );
}

export function Welcome() {
  const app = useApp();
  return (
    <div className="card">
      <Empty title="No agents connected yet">
        <p>Let agents connect to the dashboard themselves with an <b>enrollment token</b> (no inbound port needed),<br />
          or connect the dashboard to an agent's address. Create a group to share one set of rules across agents.</p>
        <div className="row" style={{ justifyContent: 'center' }}>
          <button onClick={() => app.openDialog({ kind: 'group' })}>New group</button>
          <button onClick={() => app.openDialog({ kind: 'agent' })}>Connect by address</button>
          <button className="primary" onClick={() => app.go({ kind: 'enroll' })}>Enroll agents</button>
        </div>
      </Empty>
    </div>
  );
}
