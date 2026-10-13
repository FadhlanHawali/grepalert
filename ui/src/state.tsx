import { createContext, useCallback, useContext, useEffect, useMemo, useRef, useState, type ReactNode } from 'react';
import { api, get } from './api';
import type { Agent, Group, Me, Overview, Role, Silence } from './types';

// ---------- routing (hash based: #group/web, #alerts, ...) ----------

export type View =
  | { kind: 'all' | 'ungrouped' | 'enroll' | 'alerts' | 'silences' | 'settings' | 'access' | 'audit' | 'playground' }
  | { kind: 'group'; name: string };

const PAGES = ['ungrouped', 'enroll', 'alerts', 'silences', 'settings', 'access', 'audit', 'playground'] as const;

export function parseHash(hash: string): View {
  const h = decodeURIComponent(hash.slice(1));
  if (h.startsWith('group/')) return { kind: 'group', name: h.slice(6) };
  if ((PAGES as readonly string[]).includes(h)) return { kind: h as (typeof PAGES)[number] };
  return { kind: 'all' };
}

export const viewHash = (v: View) => '#' + (v.kind === 'group' ? 'group/' + encodeURIComponent(v.name) : v.kind);

// ---------- context ----------

const rank: Record<string, number> = { viewer: 1, editor: 2 };

export interface Toast { id: number; text: string; bad?: boolean }

export type DialogState =
  | { kind: 'agent'; agent?: Agent; group?: string }
  | { kind: 'group'; group?: Group }
  | { kind: 'add-existing'; group: string };

export interface AppState {
  me: Me;
  isAdmin: boolean;
  /** Whether the user has role on group ('' = all groups). The server checks again. */
  can: (role: Role, group?: string) => boolean;

  data: Overview;
  silences: Silence[];
  loaded: boolean;
  error: string;
  refresh: () => Promise<void>;
  auto: boolean;
  setAuto: (v: boolean) => void;

  view: View;
  go: (v: View) => void;
  selAgent: string | null;
  selectAgent: (name: string | null, tab?: string) => void;
  agentTab: string;
  setAgentTab: (t: string) => void;

  groupOf: (name?: string) => Group | undefined;
  agentOf: (name?: string) => Agent | undefined;

  toast: (text: string, bad?: boolean) => void;
  toasts: Toast[];

  dialog: DialogState | null;
  openDialog: (d: DialogState) => void;
  closeDialog: () => void;
}

const Ctx = createContext<AppState | null>(null);

export function useApp(): AppState {
  const c = useContext(Ctx);
  if (!c) throw new Error('useApp outside AppProvider');
  return c;
}

/** Holds the signed-in user, the polled overview, navigation, toasts and dialogs. */
export function AppProvider({ me, children }: { me: Me; children: ReactNode }) {
  const [data, setData] = useState<Overview>({ agents: [], groups: [] });
  const [silences, setSilences] = useState<Silence[]>([]);
  const [loaded, setLoaded] = useState(false);
  const [error, setError] = useState('');
  const [auto, setAuto] = useState(true);
  const [view, setView] = useState<View>(() => parseHash(location.hash));
  const [selAgent, setSelAgent] = useState<string | null>(null);
  const [agentTab, setAgentTab] = useState('overview');
  const [toasts, setToasts] = useState<Toast[]>([]);
  const [dialog, setDialog] = useState<DialogState | null>(null);
  const toastId = useRef(0);

  const isAdmin = !!me.perms.admin;
  const can = useCallback((role: Role, group?: string) => {
    const p = me.perms;
    if (p.admin) return true;
    if ((rank[p.global || ''] || 0) >= rank[role]) return true;
    return !!group && (rank[(p.groups || {})[group] || ''] || 0) >= rank[role];
  }, [me]);

  const refresh = useCallback(async () => {
    try {
      const [ov, sil] = await Promise.all([get<Overview>('/api/overview'), get<Silence[]>('/api/silences').catch(() => [])]);
      setData(ov);
      setSilences(sil || []);
      setError('');
    } catch (e) {
      setError(e instanceof Error ? e.message : String(e));
    } finally {
      setLoaded(true);
    }
  }, []);

  useEffect(() => { refresh(); }, [refresh]);
  useEffect(() => {
    if (!auto) return;
    const t = setInterval(() => { if (!document.hidden) refresh(); }, 5000);
    return () => clearInterval(t);
  }, [auto, refresh]);

  useEffect(() => {
    const onHash = () => { setView(parseHash(location.hash)); setSelAgent(null); };
    window.addEventListener('hashchange', onHash);
    return () => window.removeEventListener('hashchange', onHash);
  }, []);

  const go = useCallback((v: View) => {
    history.replaceState(null, '', viewHash(v));
    setView(v);
    setSelAgent(null);
  }, []);

  const selectAgent = useCallback((name: string | null, tab = 'overview') => {
    setSelAgent(name);
    setAgentTab(tab);
  }, []);

  const toast = useCallback((text: string, bad?: boolean) => {
    const id = ++toastId.current;
    setToasts(t => [...t, { id, text, bad }]);
    setTimeout(() => setToasts(t => t.filter(x => x.id !== id)), bad ? 7000 : 3500);
  }, []);

  const value = useMemo<AppState>(() => ({
    me, isAdmin, can, data, silences, loaded, error, refresh, auto, setAuto,
    view, go, selAgent, selectAgent, agentTab, setAgentTab,
    groupOf: n => data.groups.find(g => g.name === n),
    agentOf: n => data.agents.find(a => a.name === n),
    toast, toasts,
    dialog, openDialog: setDialog, closeDialog: () => setDialog(null),
  }), [me, isAdmin, can, data, silences, loaded, error, refresh, auto, view, go, selAgent, selectAgent, agentTab, toast, toasts, dialog]);

  return <Ctx.Provider value={value}>{children}</Ctx.Provider>;
}

/** Loads something when deps change (keeping the last value meanwhile); reload() fetches again. */
export function useLoad<T>(fn: () => Promise<T>, deps: unknown[]): { value: T | undefined; error: string; loading: boolean; reload: () => void } {
  const [value, setValue] = useState<T>();
  const [error, setError] = useState('');
  const [loading, setLoading] = useState(true);
  const [n, setN] = useState(0);
  useEffect(() => {
    let live = true;
    setLoading(true);
    fn().then(v => { if (live) { setValue(v); setError(''); } })
      .catch(e => { if (live) setError(e instanceof Error ? e.message : String(e)); })
      .finally(() => { if (live) setLoading(false); });
    return () => { live = false; };
  }, [...deps, n]); // eslint-disable-line react-hooks/exhaustive-deps
  return { value, error, loading, reload: () => setN(x => x + 1) };
}

export async function logout() {
  // SSO users are sent on to the identity provider to end that session too.
  let to = '/login';
  try {
    const { data } = await api<{ redirect?: string } | string>('POST', '/auth/logout');
    if (data && typeof data === 'object' && data.redirect) to = data.redirect;
  } catch { /* signed out anyway */ }
  location.href = to;
}
