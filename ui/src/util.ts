import type { Labels } from './types';

export const mb = (b: number) => (b / 1048576).toFixed(1) + ' MB';
export const zero = (t?: string) => !t || t.startsWith('0001');

export function ago(t?: string): string {
  if (zero(t)) return 'never';
  const s = Math.max(0, (Date.now() - new Date(t!).getTime()) / 1000);
  if (s < 60) return Math.floor(s) + 's ago';
  if (s < 3600) return Math.floor(s / 60) + 'm ago';
  if (s < 86400) return Math.floor(s / 3600) + 'h ago';
  return Math.floor(s / 86400) + 'd ago';
}

export function uptime(t: string): string {
  let s = Math.floor((Date.now() - new Date(t).getTime()) / 1000);
  const d = Math.floor(s / 86400);
  s %= 86400;
  const h = Math.floor(s / 3600), m = Math.floor((s % 3600) / 60);
  return (d ? d + 'd ' : '') + (d || h ? h + 'h ' : '') + m + 'm';
}

/** Time left until t, e.g. "25 min" or "3 h 10 min". */
export function until(t: string): string {
  const m = Math.round((new Date(t).getTime() - Date.now()) / 60000);
  return m < 60 ? `${Math.max(m, 0)} min` : `${Math.floor(m / 60)} h ${m % 60} min`;
}

export const localTime = (t: string) => new Date(t).toLocaleString();

/** Duration strings as the server writes them ("5m", "30s") in ms. */
export function durMs(d?: string): number {
  const m = /^(\d+(?:\.\d+)?)(ms|s|m|h)$/.exec(d || '');
  return m ? Number(m[1]) * ({ ms: 1, s: 1e3, m: 6e4, h: 36e5 } as Record<string, number>)[m[2]] : 0;
}

/** Labels as "k=v, k2=v2" text and back. */
export const labelsText = (l?: Labels) => Object.entries(l || {}).map(([k, v]) => `${k}=${v}`).join(', ');
export function parseLabels(s: string): Labels {
  const out: Labels = {};
  for (const part of s.split(',').map(x => x.trim()).filter(Boolean)) {
    const i = part.indexOf('=');
    if (i <= 0) throw new Error(`"${part}" is not key=value`);
    out[part.slice(0, i).trim()] = part.slice(i + 1).trim();
  }
  return out;
}

export const splitList = (s: string) => s.split(',').map(x => x.trim()).filter(Boolean);
export const plural = (n: number, one: string, many = one + 's') => `${n} ${n === 1 ? one : many}`;

/** A plain LCS line diff ("  ", "+ ", "- " prefixes), fine for rule files. */
export function lineDiff(a: string, b: string): string | null {
  const x = a.split('\n'), y = b.split('\n');
  if (x.length * y.length > 4e6) return null;
  const n = x.length, m = y.length;
  const L = Array.from({ length: n + 1 }, () => new Uint16Array(m + 1));
  for (let i = n - 1; i >= 0; i--)
    for (let j = m - 1; j >= 0; j--) L[i][j] = x[i] === y[j] ? L[i + 1][j + 1] + 1 : Math.max(L[i + 1][j], L[i][j + 1]);
  const out: string[] = [];
  let i = 0, j = 0;
  while (i < n || j < m) {
    if (i < n && j < m && x[i] === y[j]) { out.push('  ' + x[i]); i++; j++; }
    else if (i < n && (j === m || L[i + 1][j] >= L[i][j + 1])) out.push('- ' + x[i++]); // removals first
    else out.push('+ ' + y[j++]);
  }
  return out.join('\n');
}

export const EXAMPLE_RULES = `webhooks:
  - name: alerts
    url: \${WEBHOOK_URL}      # set WEBHOOK_URL in each agent's environment
    # template: '{"text": {{ json .Text }}}'   # Slack-style body

rules:
  - name: errors
    files: ["/var/log/app/*.log"]
    pattern: '(?i)error|exception'
    severity: warning
    webhooks: [alerts]
    suppress:
      window: 5m
      max_alerts: 1
`;
