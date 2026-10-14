// Logic shared by several pages: rule sync state, rollouts, stages.
import { api, enc, errText } from './api';
import type { Agent, Group, Silence } from './types';

export const DEFAULT_STAGES = ['1', '10%', '100%'];
export const stagesOf = (g: Group) => (g.rollout?.stages?.length ? g.rollout.stages : DEFAULT_STAGES);
export const stageText = (s: string) => (s === '100%' ? 'all' : s.endsWith('%') ? s : s === '1' ? '1 agent' : s + ' agents');

/** During a staged rollout agents may run different versions. */
export const rolloutActive = (g: Group) => {
  const ro = g.rollout_state;
  return !!ro && (ro.status === 'running' || ro.status === 'halted') && ro.hash === g.rules_hash;
};
export const isCanary = (g: Group, a: Agent) =>
  rolloutActive(g) && g.rollout_state!.status === 'running' && (g.canaries || []).includes(a.name);
export const expectedHash = (g: Group, a: Agent) =>
  !rolloutActive(g) ? g.rules_hash : isCanary(g, a) ? g.rollout_state!.hash : g.rollout_state!.stable_hash;

export interface SyncState { cls: '' | 'ok' | 'warn' | 'bad'; text: string; title?: string }

/** An agent's rules relative to its group, or null for ungrouped agents. */
export function syncState(a: Agent, g: Group | undefined): SyncState | null {
  if (!a.group) return null;
  if (!g || !g.rules_hash) return { cls: '', text: 'no group rules yet' };
  if (!a.online || !a.status) return { cls: '', text: 'offline' };
  const res = g.state.results[a.name];
  if (a.status.rules_hash === expectedHash(g, a)) {
    if (!rolloutActive(g)) return { cls: 'ok', text: 'in sync' };
    return isCanary(g, a)
      ? { cls: 'ok', text: 'new version', title: 'runs ' + g.rollout_state!.hash + ' (first in the rollout)' }
      : { cls: 'ok', text: 'previous version', title: 'runs ' + g.rollout_state!.stable_hash + ' until the rollout reaches it' };
  }
  if (res && !res.ok) return { cls: 'bad', text: 'push failed', title: res.error };
  return { cls: 'warn', text: 'out of sync', title: 'will be re-pushed on the next reconcile' };
}

export function groupHealth(g: Group, agentOf: (n: string) => Agent | undefined): string {
  const members = g.members.map(agentOf).filter((a): a is Agent => !!a);
  if (g.state.last_error) return 'bad';
  if (!members.length || !g.rules_hash) return '';
  const st = members.map(a => syncState(a, g)!);
  if (st.some(s => s.cls === 'bad')) return 'bad';
  if (st.some(s => s.cls === 'warn' || s.text === 'offline')) return 'warn';
  return 'ok';
}

export function sourceText(g: Group): string {
  if (g.source === 'manual' || !g.git) return 'Edited in this dashboard';
  const x = g.git;
  if (x.provider === 'url') return 'URL ' + x.path;
  return `${x.provider === 'github' ? 'GitHub' : 'GitLab'} ${x.repo} @ ${x.ref} : ${x.path}`;
}

export const activeSilences = (all: Silence[]) =>
  all.filter(s => new Date(s.ends_at).getTime() > Date.now() && new Date(s.starts_at).getTime() <= Date.now());

export const scopeText = (s: Silence) =>
  [s.group ? `group ${s.group}` : 'all groups', s.rules?.length ? `rules ${s.rules.join(', ')}` : 'all rules',
    s.agents?.length ? `agents ${s.agents.join(', ')}` : ''].filter(Boolean).join(' · ');

/**
 * After an agent left its group, offers to bring back the rules it had
 * before the group replaced them (the agent kept a copy).
 */
export async function offerOwnRules(a: Agent, oldGroup: string | undefined, toast: (t: string, bad?: boolean) => void) {
  if (!oldGroup || !a.status?.own_rules_hash) return;
  if (!confirm(`${a.name} left group ${oldGroup} and still runs that group's rules.

Restore the agent's own rules from before it joined a group (version ${a.status.own_rules_hash})?`)) return;
  try {
    await api('POST', `/api/agents/${enc(a.name)}/own-rules/restore`);
    toast(`${a.name} runs its own rules again`);
  } catch (e) { toast(errText(e), true); }
}

export function pushSummary(results: Record<string, { ok: boolean; action: string; error?: string }>): string {
  const r = Object.entries(results || {});
  if (!r.length) return 'No agents in this group yet.';
  return r.map(([n, x]) => `${n}: ${x.ok ? x.action : 'FAILED: ' + x.error}`).join('\n');
}
