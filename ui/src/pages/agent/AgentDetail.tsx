import { useApp } from '../../state';
import type { Agent } from '../../types';
import { Card, Tabs } from '../../components/ui';
import { AgentEvents, AgentOverview, AgentTest } from './AgentTabs';
import { AgentRules } from './AgentRules';
import { AgentSettings } from './AgentSettings';

const NAMES: Record<string, string> = { overview: 'Overview', rules: 'Rules', events: 'Events', test: 'Test a line', settings: 'Settings' };

/** The selected agent, below the agent list. */
export function AgentDetail({ a }: { a: Agent }) {
  const app = useApp();
  const tabs = ['overview', 'rules', 'events', 'test', ...(app.isAdmin ? ['settings'] : [])];
  const tab = tabs.includes(app.agentTab) ? app.agentTab : 'overview';
  return (
    <div id="agent-detail">
      <Card title={a.name} sub={<span className="mono">{a.url}</span>} actions={<button onClick={() => app.selectAgent(null)}>Close</button>}>
        <Tabs value={tab} onChange={app.setAgentTab} tabs={tabs.map(t => ({ id: t, label: NAMES[t] }))} />
        <div className="pane">
          {tab === 'overview' && <AgentOverview a={a} />}
          {tab === 'rules' && <AgentRules key={a.name} a={a} />}
          {tab === 'events' && <AgentEvents key={a.name} a={a} />}
          {tab === 'test' && <AgentTest key={a.name} a={a} />}
          {tab === 'settings' && <AgentSettings key={a.name} a={a} />}
        </div>
      </Card>
    </div>
  );
}
