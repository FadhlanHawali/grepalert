// Shapes of the dashboard API's JSON.

export type Labels = Record<string, string>;

export interface RuleStatus {
  name: string;
  pattern: string;
  files: string[];
  suppress?: string;
  local?: boolean;
  absent?: string;
  for?: string;
  firing?: boolean;
  last_seen_line?: string;
  matches: number;
  alerts: number;
  suppressed: number;
  last_match?: string;
}

export interface FileStatus {
  path: string;
  open: boolean;
  offset: number;
  lines: number;
  rules?: string[];
  error?: string;
}

export interface AgentStatus {
  name: string;
  host: string;
  version: string;
  started_at: string;
  rules_hash: string;
  rules_loaded_at: string;
  rules_origin: string;
  local_rules_hash?: string;
  local_rules?: number;
  local_rules_error?: string;
  rules?: RuleStatus[];
  files?: FileStatus[];
  alerts_total?: number;
  webhooks: {
    sent: number; failed: number; dropped: number; queued: number;
    via_dashboard?: number; fallback?: number; silenced?: number;
  };
  suppress_keys: number;
  dashboard?: { connected: boolean };
  memory: { heap_inuse: number; sys: number; memory_limit: number };
}

export interface Agent {
  name: string;
  mode: 'direct' | 'connected';
  url?: string;
  group?: string;
  assigned?: boolean;
  has_token: boolean;
  insecure_skip_verify?: boolean;
  added_at: string;
  labels?: Labels;
  hostname?: string;
  enrolled_via?: string;
  revoked?: boolean;
  last_seen?: string;
  down_since?: string;
  online: boolean;
  error?: string;
  status?: AgentStatus;
}

export interface PushResult {
  ok: boolean;
  action: string;
  hash?: string;
  error?: string;
  time: string;
}

export interface GroupState {
  syncing: boolean;
  last_sync: string;
  last_fetch: string;
  last_error?: string;
  trigger?: string;
  results: Record<string, PushResult>;
}

export interface GitSource {
  provider: 'github' | 'gitlab' | 'url';
  base_url?: string;
  repo?: string;
  ref?: string;
  path: string;
  interval?: string;
  has_token?: boolean;
  token?: string;
}

export interface FleetSettings {
  disabled?: boolean;
  wait?: string;
  window?: string;
}

export interface RolloutSettings {
  enabled: boolean;
  stages?: string[];
  soak?: string;
  max_increase?: number;
  min_alerts?: number;
}

export interface Rollout {
  id: string;
  hash: string;
  stable_hash: string;
  stage: number;
  status: 'running' | 'halted' | 'done' | 'aborted';
  reason?: string;
  started_at: string;
  started_by?: string;
  stage_started: string;
  updated_at: string;
  log?: { time: string; message: string }[];
}

export interface Group {
  name: string;
  description?: string;
  source: 'manual' | 'git';
  git?: GitSource;
  selector?: Labels;
  fleet: FleetSettings;
  ops_webhook_url?: string;
  rules_hash?: string;
  rules_updated_at: string;
  members: string[];
  state: GroupState;
  rollout: RolloutSettings;
  rollout_state?: Rollout;
  canaries?: string[];
  git_paused?: string;
}

export interface Overview {
  agents: Agent[];
  groups: Group[];
}

export interface Silence {
  id: string;
  group?: string;
  agents?: string[];
  rules?: string[];
  starts_at: string;
  ends_at: string;
  reason?: string;
  created_at: string;
}

export type Role = 'viewer' | 'editor' | 'admin';

export interface Me {
  open?: boolean;
  identity: { user: string; name?: string; method: string; groups?: string[] };
  perms: { admin: boolean; global?: Role; groups?: Record<string, Role> };
}

export interface HistoryEntry {
  id: string;
  time: string;
  kind: string;
  group?: string;
  rule: string;
  group_key?: string;
  hosts?: string[];
  host_count: number;
  status: string;
  detail?: string;
  text: string;
}

export interface AuditEntry {
  time: string;
  actor: string;
  name?: string;
  method?: string;
  ip?: string;
  action: string;
  target?: string;
  group?: string;
  result: string;
  detail?: string;
  diff?: string;
}

export interface Version {
  hash: string;
  time: string;
  author?: string;
  source: string;
  note?: string;
  rules: number;
  current?: boolean;
  stable?: boolean;
}

export interface DryRunRule { name: string; matches: number; alerts: number; suppressed: number; samples?: string[] }
export interface DryRunSet { hash: string; alerts: number; rules: DryRunRule[] }
export interface DryRunFile { path: string; bytes: number; lines: number; from: string; truncated?: boolean; estimated?: boolean; error?: string }
export interface AgentDryRun { window: string; from: string; to: string; files?: DryRunFile[]; current: DryRunSet; candidate: DryRunSet; partial?: boolean }

export interface DryRunReport {
  window: string;
  agents: { agent: string; ok: boolean; error?: string; result?: AgentDryRun }[];
  agents_ok: number;
  current_alerts: number;
  candidate_alerts: number;
  rules: { name: string; current: number; candidate: number; matches: number; agents: number }[];
}
