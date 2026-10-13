const $ = (s, el = document) => el.querySelector(s);
const esc = s => String(s ?? '').replace(/[&<>"']/g, c => ({'&':'&amp;','<':'&lt;','>':'&gt;','"':'&quot;',"'":'&#39;'}[c]));
const mb = b => (b / 1048576).toFixed(1) + ' MB';
const zero = t => !t || t.startsWith('0001');
const ago = t => {
  if (zero(t)) return 'never';
  const s = Math.max(0, (Date.now() - new Date(t)) / 1000);
  if (s < 60) return Math.floor(s) + 's ago';
  if (s < 3600) return Math.floor(s / 60) + 'm ago';
  if (s < 86400) return Math.floor(s / 3600) + 'h ago';
  return Math.floor(s / 86400) + 'd ago';
};
const uptime = t => {
  let s = Math.floor((Date.now() - new Date(t)) / 1000), d = Math.floor(s / 86400); s %= 86400;
  const h = Math.floor(s / 3600), m = Math.floor(s % 3600 / 60);
  return (d ? d + 'd ' : '') + (d || h ? h + 'h ' : '') + m + 'm';
};
const enc = encodeURIComponent;

const EXAMPLE_RULES = `webhooks:
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

async function api(method, path, body, type) {
  const opts = { method, headers: { 'X-Requested-With': 'grepalert' } };
  if (body !== undefined) {
    opts.body = typeof body === 'string' ? body : JSON.stringify(body);
    opts.headers['Content-Type'] = type || 'application/json';
  }
  // Absolute URL from origin: a page opened as http://user:pass@host/ would
  // otherwise resolve relative URLs with credentials, which fetch() rejects.
  const r = await fetch(location.origin + path, opts);
  const ct = r.headers.get('Content-Type') || '';
  const data = ct.includes('json') ? await r.json() : await r.text();
  if (r.status === 401) { location.href = '/login?next=' + encodeURIComponent(location.pathname + location.hash); throw new Error('signed out'); }
  if (!r.ok) { const e = new Error((data && data.error) || data || ('HTTP ' + r.status)); e.data = data; throw e; }
  return { data, headers: r.headers };
}

function toast(text, bad) {
  const d = document.createElement('div');
  d.textContent = text; if (bad) d.className = 'bad';
  $('#toast').append(d); setTimeout(() => d.remove(), bad ? 7000 : 3500);
}

// ---------- state ----------
let data = { agents: [], groups: [] };
let view = parseHash();            // {kind: 'all'|'ungrouped'|'group', name}
let selAgent = null, agentTab = 'overview', groupTab = 'agents';
let editorDirty = false, editorKey = null;
let editingAgent = null, editingGroup = null;

function parseHash() {
  const h = decodeURIComponent(location.hash.slice(1));
  if (h.startsWith('group/')) return { kind: 'group', name: h.slice(6) };
  if (h === 'ungrouped') return { kind: 'ungrouped' };
  if (h === 'enroll') return { kind: 'enroll' };
  if (['alerts', 'silences', 'settings', 'access', 'audit'].includes(h)) return { kind: h };
  return { kind: 'all' };
}
function go(v) {
  view = v; selAgent = null; editorDirty = false; groupTab = 'agents';
  if (v.kind !== 'enroll') newToken = null;
  history.replaceState(null, '', '#' + (v.kind === 'group' ? 'group/' + enc(v.name) : v.kind));
  render();
}
const groupOf = n => data.groups.find(g => g.name === n);
const agentOf = n => data.agents.find(a => a.name === n);

// Labels as "k=v, k2=v2" text and back.
const labelsText = l => Object.entries(l || {}).map(([k, v]) => `${k}=${v}`).join(', ');
function parseLabels(s) {
  const out = {};
  for (const part of s.split(',').map(x => x.trim()).filter(Boolean)) {
    const i = part.indexOf('=');
    if (i <= 0) throw new Error(`"${part}" is not key=value`);
    out[part.slice(0, i).trim()] = part.slice(i + 1).trim();
  }
  return out;
}
const labelChips = l => Object.entries(l || {}).filter(([k]) => k !== 'os')
  .map(([k, v]) => `<span class="badge">${esc(k)}=${esc(v)}</span>`).join(' ');

// The name cell of an agent row: status dot, name, how it is connected.
function agentNameCell(a) {
  const dot = a.revoked ? '' : a.online ? 'ok' : 'bad';
  const how = a.mode === 'connected'
    ? `<span title="Connects out to the dashboard: no inbound port">⇡ ${esc(a.hostname || 'enrolled agent')}</span>`
    : esc(a.url);
  const auto = a.mode === 'connected' && a.group && !a.assigned ? ' <span class="badge" title="Joined by label">auto</span>' : '';
  const down = !zero(a.down_since) ? ` <span class="badge bad" title="agent-down alert sent; down since ${esc(new Date(a.down_since).toLocaleString())}">down</span>` : '';
  return `<span class="dot ${dot}"></span> ${esc(a.name)}${auto}${down}<div class="muted mono">${how}</div>
    ${a.labels ? `<div style="margin-top:3px">${labelChips(a.labels)}</div>` : ''}`;
}

function offlineText(a) {
  if (a.revoked) return '<span class="badge bad">revoked</span> <span class="muted">The dashboard refuses this agent until you allow it again (Settings).</span>';
  if (a.mode === 'connected') return `<span class="badge bad">offline</span> <span class="muted">${zero(a.last_seen) ? 'has not connected since the dashboard started' : 'last seen ' + ago(a.last_seen)}</span>`;
  return `<span class="badge bad">offline</span> <span class="muted">${esc(a.error)}</span>`;
}

// Rule state of an agent relative to its group.
function syncState(a) {
  if (!a.group) return null;
  const g = groupOf(a.group);
  if (!g || !g.rules_hash) return { cls: '', text: 'no group rules yet' };
  const res = g.state.results[a.name];
  if (!a.online) return { cls: '', text: 'offline' };
  if (a.status.rules_hash === g.rules_hash) return { cls: 'ok', text: 'in sync' };
  if (res && !res.ok) return { cls: 'bad', text: 'push failed', title: res.error };
  return { cls: 'warn', text: 'out of sync', title: 'will be re-pushed on the next reconcile' };
}

function groupHealth(g) {
  const members = g.members.map(agentOf).filter(Boolean);
  if (g.state.last_error) return 'bad';
  if (!members.length || !g.rules_hash) return '';
  const st = members.map(syncState);
  if (st.some(s => s.cls === 'bad')) return 'bad';
  if (st.some(s => s.cls === 'warn' || s.text === 'offline')) return 'warn';
  return 'ok';
}

// ---------- rendering ----------
function render() {
  renderNav(); renderMain();
  $('#main').dataset.view = view.kind;
  const online = data.agents.filter(a => a.online).length;
  $('#summary').textContent = data.agents.length ? `${online}/${data.agents.length} agents online` : '';
}

function renderNav() {
  const ungrouped = data.agents.filter(a => !a.group).length;
  const on = (k, n) => view.kind === k && (k !== 'group' || view.name === n) ? 'on' : '';
  $('#nav').innerHTML = `
    <a class="${on('all')}" data-nav="all">All agents <span class="count">${data.agents.length}</span></a>
    <a class="${on('ungrouped')}" data-nav="ungrouped">Ungrouped <span class="count">${ungrouped}</span></a>
    <div class="label">Groups</div>
    ${data.groups.map(g => `<a class="${on('group', g.name)}" data-nav="group" data-name="${esc(g.name)}">
      <span class="dot ${groupHealth(g)}"></span>${esc(g.name)}<span class="count">${g.members.length}</span></a>`).join('')}
    ${isAdmin() ? '<a class="new" data-action="new-group">+ New group</a>' : ''}
    <div class="label">Alerts</div>
    <a class="${on('alerts')}" data-nav="alerts">Alert history</a>
    <a class="${on('silences')}" data-nav="silences">Silences ${activeSilences().length ? `<span class="count">${activeSilences().length} active</span>` : ''}</a>
    ${isAdmin() ? `<div class="label">Setup</div>
    <a class="${on('enroll')}" data-nav="enroll">Agent enrollment</a>
    <a class="${on('access')}" data-nav="access">Users &amp; access</a>
    <a class="${on('audit')}" data-nav="audit">Audit log</a>
    <a class="${on('settings')}" data-nav="settings">Settings</a>` : ''}`;
}

function renderMain() {
  const main = $('#main');
  if (view.kind === 'enroll') { renderEnroll(); return; }
  if (view.kind === 'alerts') { renderAlerts(); return; }
  if (view.kind === 'silences') { renderSilences(); return; }
  if (view.kind === 'settings') { renderSettings(); return; }
  if (view.kind === 'access') { renderAccess(); return; }
  if (view.kind === 'audit') { renderAudit(); return; }
  if (!data.agents.length && !data.groups.length) {
    main.innerHTML = `<div class="card"><div class="empty"><h3>No agents connected yet</h3>
      <p>Let agents connect to the dashboard themselves with an <b>enrollment token</b> (no inbound port needed),<br>
      or connect the dashboard to an agent's address. Create a group to share one set of rules across agents.</p>
      <div class="row" style="justify-content:center"><button data-action="new-group">New group</button>
      <button data-action="connect">Connect by address</button>
      <button class="primary" data-nav="enroll">Enroll agents</button></div></div></div>`;
    return;
  }
  let html = '';
  if (view.kind === 'group') {
    const g = groupOf(view.name);
    if (!g) { go({ kind: 'all' }); return; }
    html += groupCard(g);
  } else {
    const list = view.kind === 'ungrouped' ? data.agents.filter(a => !a.group) : data.agents;
    html += `<div class="card"><div class="card-head"><div><h2>${view.kind === 'ungrouped' ? 'Ungrouped agents' : 'All agents'}</h2>
      <div class="sub">${view.kind === 'ungrouped' ? 'Each of these agents keeps its own rules. Add them to a group to manage rules together.' : 'Every agent connected to this dashboard.'}</div></div>
      <div class="actions">${isAdmin() ? '<button class="primary" data-action="connect">Connect agent</button>' : ''}</div></div>
      ${agentTable(list, view.kind === 'all')}</div>`;
  }
  html += `<div id="agent-detail"></div>`;
  main.innerHTML = html;
  if (view.kind === 'group' && groupTab === 'rules') loadGroupRules();
  renderAgentDetail();
}

function agentTable(list, showGroup, groupCtx) {
  if (!list.length) return `<div class="empty">${groupCtx ? 'No agents in this group yet.' : 'No agents here.'}${isAdmin() ? `<div class="row" style="justify-content:center">
    <button class="primary" data-action="connect" ${groupCtx ? `data-group="${esc(groupCtx)}"` : ''}>Connect agent</button>
    ${groupCtx && data.agents.some(a => a.group !== groupCtx) ? `<button data-action="add-existing" data-group="${esc(groupCtx)}">Add an existing agent</button>` : ''}</div>` : ''}</div>`;
  return `<div class="scroll"><table><thead><tr>
    <th>Agent</th>${showGroup ? '<th>Group</th>' : ''}<th>Rules</th><th>Host</th><th>Version</th><th>Uptime</th>
    <th class="num">Files</th><th class="num">Alerts</th><th class="num">Failed / dropped</th><th class="num">Memory</th></tr></thead><tbody>
    ${list.map(a => {
      const sel = selAgent === a.name ? 'sel' : '';
      const group = showGroup ? `<td>${a.group ? `<a class="badge" data-nav="group" data-name="${esc(a.group)}" style="cursor:pointer">${esc(a.group)}</a>` : '<span class="muted">—</span>'}</td>` : '';
      if (!a.online) return `<tr class="click ${sel}" data-agent="${esc(a.name)}"><td>${agentNameCell(a)}</td>${group}
        <td colspan="8" class="wrap">${offlineText(a)}</td></tr>`;
      const s = a.status, st = syncState(a);
      const rules = st ? `<span class="badge ${st.cls}" title="${esc(st.title || '')}">${esc(st.text)}</span>` : `<code>${esc(s.rules_hash)}</code> <span class="muted">${(s.rules || []).length} rules</span>`;
      const bad = s.webhooks.failed + s.webhooks.dropped;
      return `<tr class="click ${sel}" data-agent="${esc(a.name)}">
        <td>${agentNameCell(a)}</td>${group}
        <td>${rules}</td><td>${esc(s.host)}</td><td>${esc(s.version)}</td><td>${uptime(s.started_at)}</td>
        <td class="num">${(s.files || []).length}</td><td class="num" title="sent by the agent: ${s.webhooks.sent}, handed to the dashboard: ${s.webhooks.via_dashboard || 0}">${s.webhooks.sent + (s.webhooks.via_dashboard || 0)}</td>
        <td class="num">${bad ? `<span class="badge bad">${s.webhooks.failed} / ${s.webhooks.dropped}</span>` : '0 / 0'}</td>
        <td class="num">${mb(s.memory.sys)}</td></tr>`;
    }).join('')}</tbody></table></div>`;
}

function sourceText(g) {
  if (g.source === 'manual') return 'Edited in this dashboard';
  const x = g.git;
  if (x.provider === 'url') return 'URL ' + x.path;
  return `${x.provider === 'github' ? 'GitHub' : 'GitLab'} ${x.repo} @ ${x.ref} : ${x.path}`;
}

function groupCard(g) {
  const st = g.state, members = g.members.map(agentOf).filter(Boolean);
  const inSync = members.filter(a => syncState(a)?.cls === 'ok').length;
  return `<div class="card">
    <div class="card-head"><div><h2>${esc(g.name)}</h2><div class="sub">${esc(g.description || '')}</div></div>
      <div class="actions">
        ${can('editor', g.name) ? `<button data-action="sync-group" ${st.syncing ? 'disabled' : ''}>${st.syncing ? 'Syncing…' : g.source === 'git' ? 'Sync from git' : 'Push to agents'}</button>
        <button data-action="silence-group">Silence…</button>` : ''}
        ${isAdmin() ? `<button data-action="edit-group">Edit</button>
        <button class="danger" data-action="delete-group">Delete</button>` : ''}</div></div>
    <div class="meta">
      <span>Rules: <b>${esc(sourceText(g))}</b></span>
      <span>Fleet: <b>${g.fleet && g.fleet.disabled ? 'off (every agent alerts separately)' : `one alert across hosts, gathered ${esc((g.fleet && g.fleet.wait) || '5s')}, repeats held ${esc((g.fleet && g.fleet.window) || '5m')}`}</b></span>
      <span>Members: <b>${g.selector ? 'agents labelled ' + esc(labelsText(g.selector)) + ', plus any added by hand' : 'added by hand'}</b></span>
      ${g.source === 'git' ? `<span>Sync: <b>${g.git.interval ? 'every ' + esc(g.git.interval) : 'manual'}</b></span>` : ''}
      <span>Version: <b class="mono">${g.rules_hash ? esc(g.rules_hash) : 'none yet'}</b>${g.rules_hash ? ' · updated ' + ago(g.rules_updated_at) : ''}</span>
      <span>Agents in sync: <b>${inSync}/${members.length}</b></span>
      ${!zero(st.last_sync) ? `<span>Last sync: <b>${ago(st.last_sync)}</b> (${esc(st.trigger)})</span>` : ''}
    </div>
    ${silenceBanner(g.name)}
    ${st.last_error ? `<div class="pane" style="padding-top:0"><div class="msg bad">${esc(st.last_error)}</div></div>` : ''}
    ${!g.rules_hash && g.source === 'manual' ? `<div class="pane" style="padding-top:0"><div class="msg info">This group has no rules yet. Open the <b>Rules</b> tab to write them.</div></div>` : ''}
    <div class="tabs" data-tabs="group">
      <button data-gtab="agents" class="${groupTab === 'agents' ? 'on' : ''}">Agents (${members.length})</button>
      <button data-gtab="rules" class="${groupTab === 'rules' ? 'on' : ''}">Rules</button>
    </div>
    ${groupTab === 'agents' ? `
      ${members.length && isAdmin() ? `<div class="pane" style="padding-bottom:0"><div class="row" style="margin:0">
        <button class="primary" data-action="connect" data-group="${esc(g.name)}">Connect agent</button>
        ${data.agents.some(a => a.group !== g.name) ? `<button data-action="add-existing" data-group="${esc(g.name)}">Add an existing agent</button>` : ''}</div></div><div style="height:12px"></div>` : ''}
      ${agentTable(members, false, g.name)}` : `
      <div class="pane">
        ${g.source === 'git' ? `<div class="msg info" style="margin:0 0 10px">These rules come from git and are read-only here. Change the file in the repository, then click <b>Sync from git</b>.</div>` : ''}
        <textarea id="g-editor" spellcheck="false" ${g.source === 'git' || !can('editor', g.name) ? 'readonly' : ''} placeholder="${g.source === 'git' ? 'Not synced yet' : ''}"></textarea>
        <div class="row">
          ${g.source === 'manual' && can('editor', g.name) ? `<button data-action="validate-group">Validate</button>
            <button class="primary" data-action="save-group-rules">Save &amp; push to ${members.length} agent${members.length === 1 ? '' : 's'}</button>
            <button data-action="reload-group-rules">Discard changes</button>` :
            `<button data-action="preview-git">Show latest from git</button>`}
          <span class="muted mono" id="g-hash"></span>
        </div>
        <div id="g-msg"></div><div id="g-preview"></div>
      </div>`}
  </div>`;
}

async function loadGroupRules(force) {
  const key = 'group:' + view.name;
  if (!force && editorDirty && editorKey === key) return;
  const ed = $('#g-editor'); if (!ed) return;
  try {
    const { data: text, headers } = await api('GET', `/api/groups/${enc(view.name)}/rules`);
    const g = groupOf(view.name);
    ed.value = text || (g && g.source === 'manual' && !text ? '' : text);
    $('#g-hash').textContent = headers.get('X-Rules-Hash') ? 'version ' + headers.get('X-Rules-Hash') : '';
    editorDirty = false; editorKey = key;
  } catch (e) { $('#g-msg').innerHTML = `<div class="msg bad">${esc(e.message)}</div>`; }
}

function pushSummary(state) {
  const r = Object.entries(state.results || {});
  if (!r.length) return 'No agents in this group yet.';
  return r.map(([n, x]) => `${n}: ${x.ok ? x.action : 'FAILED: ' + x.error}`).join('\n');
}

// ---------- agent detail ----------
function renderAgentDetail() {
  const el = $('#agent-detail'); if (!el) return;
  const a = selAgent && agentOf(selAgent);
  if (!a) { el.innerHTML = ''; return; }
  const tabs = ['overview', 'rules', 'events', 'test', ...(isAdmin() ? ['settings'] : [])];
  const names = { overview: 'Overview', rules: 'Rules', events: 'Events', test: 'Test a line', settings: 'Settings' };
  el.innerHTML = `<div class="card">
    <div class="card-head"><div><h2>${esc(a.name)}</h2><div class="sub mono">${esc(a.url)}</div></div>
      <div class="actions"><button data-action="close-agent">Close</button></div></div>
    <div class="tabs">${tabs.map(t => `<button data-atab="${t}" class="${agentTab === t ? 'on' : ''}">${names[t]}</button>`).join('')}</div>
    <div class="pane" id="atab"></div></div>`;
  ({ overview: tabOverview, rules: tabRules, events: tabEvents, test: tabTest, settings: tabSettings })[agentTab](a);
}

function tabOverview(a) {
  const el = $('#atab');
  if (!a.online) { el.innerHTML = `<div class="msg bad">Cannot reach this agent: ${esc(a.error)}</div><p class="muted">Check the URL and token under <b>Settings</b>.</p>`; return; }
  const s = a.status;
  const stat = (k, v) => `<div class="stat"><div class="k">${k}</div><div class="v">${v}</div></div>`;
  el.innerHTML = `<div class="grid">
      ${stat('Heap in use', mb(s.memory.heap_inuse))}${stat('Memory from OS', mb(s.memory.sys))}
      ${stat('Memory limit', s.memory.memory_limit > 0 && s.memory.memory_limit < 9e18 ? mb(s.memory.memory_limit) : 'none')}
      ${s.dashboard ? stat('Alerts via dashboard', s.webhooks.via_dashboard) + stat('Sent directly (fallback)', s.webhooks.fallback) : ''}${stat('Alerts sent', s.webhooks.sent)}${stat('Webhook queue', s.webhooks.queued)}${stat('Suppression keys', s.suppress_keys)}
    </div>
    <div class="muted">Rules <code>${esc(s.rules_hash)}</code> loaded ${ago(s.rules_loaded_at)} from <b>${esc(s.rules_origin || '—')}</b></div>
    <h3 class="sec">Rules</h3>
    <div class="scroll"><table><thead><tr><th>Rule</th><th>Pattern</th><th>Suppression</th><th class="num">Matches</th><th class="num">Alerts</th><th class="num">Suppressed</th><th>Last match</th></tr></thead><tbody>
    ${(s.rules || []).map(r => `<tr><td>${esc(r.name)}</td><td class="wrap">${r.absent ? `<span class="badge ${r.firing ? 'bad' : 'ok'}">${r.firing ? 'missing' : 'seen'}</span> absent <code>${esc(r.absent)}</code> for ${esc(r.for)}` : `<code>${esc(r.pattern)}</code>`}</td><td>${esc(r.suppress || (r.absent ? 'once, then on recovery' : 'off'))}</td>
      <td class="num">${r.matches}</td><td class="num">${r.alerts}</td><td class="num">${r.suppressed}</td><td>${ago(r.last_match)}</td></tr>`).join('') || '<tr><td colspan="7" class="muted">No active rules</td></tr>'}
    </tbody></table></div>
    <h3 class="sec">Tailed files</h3>
    <div class="scroll"><table><thead><tr><th>File</th><th>State</th><th class="num">Offset</th><th class="num">Lines read</th><th>Rules</th></tr></thead><tbody>
    ${(s.files || []).map(f => `<tr><td class="wrap"><code>${esc(f.path)}</code></td>
      <td>${f.open ? '<span class="badge ok">tailing</span>' : `<span class="badge warn" title="${esc(f.error)}">${f.error ? 'error' : 'waiting for file'}</span>`}</td>
      <td class="num">${f.offset}</td><td class="num">${f.lines}</td><td class="wrap">${(f.rules || []).map(esc).join(', ')}</td></tr>`).join('') || '<tr><td colspan="5" class="muted">No files</td></tr>'}
    </tbody></table></div>`;
}

async function tabRules(a) {
  const el = $('#atab');
  const managed = !!a.group || !can('editor', '');
  el.innerHTML = `${managed ? `<div class="msg info" style="margin:0 0 10px">This agent's rules are managed by group <b>${esc(a.group)}</b>. To change them, edit the group's rules or move the agent out of the group.</div>` : ''}
    <textarea id="a-editor" spellcheck="false" ${managed ? 'readonly' : ''}></textarea>
    <div class="row">${managed ? `<button data-nav="group" data-name="${esc(a.group)}">Open group ${esc(a.group)}</button>` : `
      <button data-action="validate-agent">Validate</button><button class="primary" data-action="save-agent-rules">Save &amp; apply</button>
      <button data-action="reload-agent-rules">Discard changes</button>`}<span class="muted mono" id="a-hash"></span></div><div id="a-msg"></div>`;
  loadAgentRules(true);
}

async function loadAgentRules(force) {
  const key = 'agent:' + selAgent;
  if (!force && editorDirty && editorKey === key) return;
  try {
    const { data: text, headers } = await api('GET', `/api/agents/${enc(selAgent)}/rules`);
    $('#a-editor').value = text; $('#a-hash').textContent = 'version ' + (headers.get('X-Rules-Hash') || '');
    editorDirty = false; editorKey = key;
  } catch (e) { $('#a-msg').innerHTML = `<div class="msg bad">${esc(e.message)}</div>`; }
}

async function tabEvents(a) {
  const el = $('#atab');
  try {
    const { data: ev } = await api('GET', `/api/agents/${enc(a.name)}/events?limit=200`);
    const cls = { alert: 'warn', webhook_error: 'bad', error: 'bad', rules: 'ok' };
    el.innerHTML = ev.length ? `<div class="scroll"><table><thead><tr><th>Time</th><th>Type</th><th>Rule</th><th>Message</th></tr></thead><tbody>
      ${ev.map(e => `<tr><td title="${esc(e.time)}">${new Date(e.time).toLocaleString()}</td><td><span class="badge ${cls[e.type] || ''}">${esc(e.type)}</span></td>
        <td>${esc(e.rule)}</td><td class="wrap mono">${esc(e.message)}</td></tr>`).join('')}</tbody></table></div>` : '<div class="empty">No events yet.</div>';
  } catch (e) { el.innerHTML = `<div class="msg bad">${esc(e.message)}</div>`; }
}

function tabTest(a) {
  $('#atab').innerHTML = `<input type="text" id="t-line" placeholder="Paste a log line to see which of this agent's rules match it">
    <div class="row"><button class="primary" data-action="run-test">Test</button></div><div id="t-msg"></div>`;
}

function tabSettings(a) {
  if (a.mode === 'connected') return tabSettingsConnected(a);
  $('#atab').innerHTML = `<div class="grid" style="grid-template-columns:repeat(auto-fill,minmax(220px,1fr))">
      <div class="stat"><div class="k">URL</div><div class="mono">${esc(a.url)}</div></div>
      <div class="stat"><div class="k">Group</div><div>${a.group ? esc(a.group) : '<span class="muted">none</span>'}</div></div>
      <div class="stat"><div class="k">Token</div><div>${a.has_token ? 'set' : '<span class="muted">none</span>'}</div></div>
      <div class="stat"><div class="k">Connected</div><div>${ago(a.added_at)}</div></div></div>
    <div class="row"><button class="primary" data-action="edit-agent">Edit connection / group</button>
    <span class="spacer"></span><button class="danger" data-action="remove-agent">Remove from dashboard</button></div>
    <p class="muted">Removing only disconnects the dashboard. The agent keeps running with its current rules.</p>`;
}

function tabSettingsConnected(a) {
  const stat = (k, v) => `<div class="stat"><div class="k">${k}</div><div>${v}</div></div>`;
  const byLabels = data.groups.find(g => g.selector && Object.entries(g.selector).every(([k, v]) => (a.labels || {})[k] === v));
  $('#atab').innerHTML = `<div class="grid" style="grid-template-columns:repeat(auto-fill,minmax(220px,1fr))">
      ${stat('Connection', '⇡ connects out to the dashboard (no inbound port)')}
      ${stat('Hostname', esc(a.hostname || '—'))}
      ${stat('Last seen', a.online ? 'connected now' : zero(a.last_seen) ? 'not since the dashboard started' : ago(a.last_seen))}
      ${stat('Enrolled', `${ago(a.added_at)} with token <b>${esc(a.enrolled_via || '—')}</b>`)}
      ${stat('Labels', labelChips(a.labels) || '<span class="muted">none</span>')}</div>
    <div class="field" style="max-width:420px"><span>Group</span>
      <select id="a-group">
        <option value="">Automatic by labels${byLabels ? ` (currently ${esc(byLabels.name)})` : ' (no group matches)'}</option>
        ${data.groups.map(g => `<option value="${esc(g.name)}" ${a.assigned && a.group === g.name ? 'selected' : ''}>${esc(g.name)}</option>`).join('')}
      </select>
      <small>Pick a group to override the labels. The group's rules replace the agent's rules.</small></div>
    <div class="row"><button class="primary" data-action="save-agent-group">Save group</button>
      <span class="spacer"></span>
      ${a.revoked ? `<button data-action="allow-agent">Allow again</button>` : `<button class="danger" data-action="revoke-agent">Revoke access</button>`}
      <button class="danger" data-action="remove-agent">Remove</button></div>
    <p class="muted"><b>Revoke</b> blocks this agent: the dashboard refuses its connection and stops managing it, until you allow it again.
      <b>Remove</b> deletes it from the list; if it still has a valid enrollment token it will enroll again.</p>`;
}

// ---------- enrollment ----------
let newToken = null; // the token just created; shown once

function renderEnroll() {
  const origin = location.origin;
  $('#main').innerHTML = `<div class="card">
    <div class="card-head"><div><h2>Agent enrollment</h2>
      <div class="sub">Agents enroll with a token, then keep an outbound connection to this dashboard. Your servers need no inbound port, and it works behind NAT and HTTP proxies.
      Give agents <b>labels</b> and groups can add them automatically.</div></div></div>
    <div class="pane" style="padding-top:0">
      <div class="row" style="margin:0;align-items:flex-end">
        <label class="field" style="flex:1;min-width:200px"><span>Token name</span><input type="text" id="tok-name" placeholder="prod-web-images"></label>
        <label class="field" style="width:200px"><span>Expires</span><select id="tok-ttl">
          <option value="">Never</option><option value="1h">In 1 hour</option><option value="24h" selected>In 1 day</option>
          <option value="168h">In 7 days</option><option value="720h">In 30 days</option></select></label>
        <button class="primary" data-action="create-token">Create token</button>
      </div>
      <div id="tok-new">${newToken ? newTokenBox(newToken, origin) : ''}</div>
      <h3 class="sec">Tokens</h3>
      <div id="tok-list" class="muted">Loading…</div>
      <h3 class="sec">How an agent connects</h3>
      <pre class="preview mono">${esc(agentSnippet(origin, '<enrollment token>'))}</pre>
      <p class="muted">The token is only used once. On first contact the agent receives its own credential and saves it to <code>identity_file</code>.
        Revoking a token stops new enrollments; agents that already enrolled keep working. To cut off an agent, revoke the agent itself.</p>
    </div></div>`;
  loadTokens();
}

function agentSnippet(origin, token) {
  return `# /etc/grepalert/agent.yaml
dashboard:
  url: ${origin}
  enrollment_token: ${token}
labels:            # groups can auto-add agents by label
  role: web
  env: prod
rules_file: /var/lib/grepalert/rules.yaml
# name: web-01     # defaults to the hostname`;
}

function newTokenBox(t, origin) {
  return `<div class="msg ok" style="font-family:inherit;white-space:normal">
    <b>Token "${esc(t.name)}" created.</b> Copy it now: it will not be shown again.
    <div class="row"><code style="user-select:all;word-break:break-all;flex:1">${esc(t.token)}</code>
      <button data-action="copy" data-text="${esc(t.token)}">Copy token</button>
      <button data-action="copy" data-text="${esc(agentSnippet(origin, t.token))}">Copy agent config</button></div></div>`;
}

async function loadTokens() {
  try {
    const { data: r } = await api('GET', '/api/tokens');
    const status = t => t.revoked ? '<span class="badge bad">revoked</span>' : t.expired ? '<span class="badge warn">expired</span>' : '<span class="badge ok">active</span>';
    const rows = r.tokens.slice().reverse().map(t => `<tr><td><b>${esc(t.name)}</b></td><td class="mono">${esc(t.prefix)}…</td>
      <td>${ago(t.created_at)}</td><td>${zero(t.expires_at) ? 'never' : new Date(t.expires_at).toLocaleString()}</td>
      <td class="num">${t.uses}</td><td>${zero(t.last_used) ? '—' : ago(t.last_used)}</td><td>${status(t)}</td>
      <td>${t.revoked ? '' : `<button class="danger" data-action="revoke-token" data-id="${esc(t.id)}">Revoke</button>`}</td></tr>`).join('');
    $('#tok-list').innerHTML = (r.config_token ? `<div class="msg info" style="margin:0 0 10px">A fixed token is also set in the dashboard config (<code>enrollment_token</code>), for automation. Agents enrolled with it show "config".</div>` : '') +
      (rows ? `<div class="scroll"><table><thead><tr><th>Name</th><th>Token</th><th>Created</th><th>Expires</th><th class="num">Agents enrolled</th><th>Last used</th><th>Status</th><th></th></tr></thead><tbody>${rows}</tbody></table></div>`
        : '<div class="empty" style="padding:16px">No tokens yet.</div>');
  } catch (e) { $('#tok-list').innerHTML = `<div class="msg bad">${esc(e.message)}</div>`; }
}

// ---------- silences, alert history, settings ----------
let silences = [];
const activeSilences = () => silences.filter(s => new Date(s.ends_at) > Date.now() && new Date(s.starts_at) <= Date.now());
const until = t => { const m = Math.round((new Date(t) - Date.now()) / 60000); return m < 60 ? `${Math.max(m, 0)} min` : `${Math.floor(m / 60)} h ${m % 60} min`; };
const scopeText = s => [s.group ? `group ${s.group}` : 'all groups', s.rules?.length ? `rules ${s.rules.join(', ')}` : 'all rules',
  s.agents?.length ? `agents ${s.agents.join(', ')}` : ''].filter(Boolean).join(' · ');

function silenceBanner(group) {
  const act = activeSilences().filter(s => !s.group || s.group === group);
  if (!act.length) return '';
  return `<div class="pane" style="padding-top:0">${act.map(s => `<div class="msg info" style="margin:0 0 6px">🔕 <b>Silenced</b> (${esc(scopeText(s))}) for another ${until(s.ends_at)}${s.reason ? ' — ' + esc(s.reason) : ''}
    <button class="link" data-action="end-silence" data-id="${esc(s.id)}">End now</button></div>`).join('')}</div>`;
}

function renderSilences() {
  const now = Date.now();
  const act = silences.filter(s => new Date(s.ends_at) > now), past = silences.filter(s => new Date(s.ends_at) <= now).slice(0, 20);
  const row = (s, active) => `<tr><td>${esc(scopeText(s))}</td><td class="wrap">${esc(s.reason || '—')}</td>
    <td>${new Date(s.starts_at).toLocaleString()}</td><td>${active ? 'in ' + until(s.ends_at) : new Date(s.ends_at).toLocaleString()}</td>
    <td>${active ? `<button data-action="end-silence" data-id="${esc(s.id)}">End now</button>` : ''}</td></tr>`;
  $('#main').innerHTML = `<div class="card"><div class="card-head"><div><h2>Silences</h2>
      <div class="sub">Mute alerts during maintenance. Silenced alerts are not sent but still appear in the alert history.
      Agents also receive silences, so they hold even when an agent has to send alerts itself.</div></div></div>
    <div class="pane" style="padding-top:0"><div class="stack" style="max-width:720px">
      <div class="two">
        <label class="field"><span>Group</span><select id="sl-group">${can('editor', '') ? '<option value="">All groups</option>' : ''}${data.groups.filter(g => can('editor', g.name)).map(g => `<option>${esc(g.name)}</option>`).join('')}</select></label>
        <label class="field"><span>For</span><select id="sl-dur"><option value="30m">30 minutes</option><option value="1h">1 hour</option><option value="2h" selected>2 hours</option><option value="4h">4 hours</option><option value="8h">8 hours</option><option value="24h">1 day</option><option value="72h">3 days</option></select></label>
      </div>
      <div class="two">
        <label class="field"><span>Only rules <span class="muted">(optional)</span></span><input type="text" id="sl-rules" placeholder="errors, panics, agent-down"><small>Comma-separated. Empty = all rules. <code>agent-down</code> silences agent-down alerts.</small></label>
        <label class="field"><span>Only agents <span class="muted">(optional)</span></span><input type="text" id="sl-agents" placeholder="db-01, db-02"><small>Comma-separated. Empty = all agents.</small></label>
      </div>
      <label class="field"><span>Reason</span><input type="text" id="sl-reason" placeholder="kernel patching, ticket OPS-123"></label>
      <div><button class="primary" data-action="create-silence">Create silence</button></div>
    </div>
    <h3 class="sec">Active</h3>
    ${act.length ? `<div class="scroll"><table><thead><tr><th>Scope</th><th>Reason</th><th>Started</th><th>Ends</th><th></th></tr></thead><tbody>${act.map(s => row(s, true)).join('')}</tbody></table></div>` : '<div class="muted">None.</div>'}
    <h3 class="sec">Recently ended</h3>
    ${past.length ? `<div class="scroll"><table><thead><tr><th>Scope</th><th>Reason</th><th>Started</th><th>Ended</th><th></th></tr></thead><tbody>${past.map(s => row(s, false)).join('')}</tbody></table></div>` : '<div class="muted">None.</div>'}
    </div></div>`;
}

let alertsGroup = '';
async function renderAlerts() {
  if (!$('#al-list')) $('#main').innerHTML = `<div class="card"><div class="card-head"><div><h2>Alert history</h2>
      <div class="sub">Alerts the dashboard handled for agents that connect to it: sent (combined across hosts), silenced, failed, and agent-down/up.
      Alerts an agent sent itself (direct agents, or while the dashboard was unreachable) are in that agent's Events tab.</div></div>
      <div class="actions"><select id="al-group"><option value="">All groups</option>${data.groups.map(g => `<option ${g.name === alertsGroup ? 'selected' : ''}>${esc(g.name)}</option>`).join('')}</select></div></div>
    <div id="al-list" class="pane muted">Loading…</div></div>`;
  try {
    const { data: list } = await api('GET', `/api/alerts?limit=300&group=${enc(alertsGroup)}`);
    const cls = { sent: 'ok', silenced: '', failed: 'bad', dropped: 'bad', 'no destination': 'warn' };
    const kinds = { match: 'match', summary: 'agent summary', fleet_summary: 'held back', absent: 'missing line', recovered: 'recovered', agent_down: 'agent down', agent_up: 'agent back' };
    $('#al-list').classList.remove('muted');
    $('#al-list').innerHTML = list.length ? `<div class="scroll"><table><thead><tr><th>Time</th><th>Status</th><th>Kind</th><th>Group</th><th>Rule</th><th class="num">Hosts</th><th>Message</th></tr></thead><tbody>
      ${list.map(e => `<tr><td title="${esc(e.time)}">${new Date(e.time).toLocaleTimeString()}</td>
        <td><span class="badge ${cls[e.status] ?? ''}" title="${esc(e.detail || '')}">${esc(e.status)}</span></td>
        <td>${esc(kinds[e.kind] || e.kind)}</td><td>${esc(e.group || '—')}</td><td>${esc(e.rule)}${e.group_key ? ` <span class="muted">(${esc(e.group_key)})</span>` : ''}</td>
        <td class="num" title="${esc((e.hosts || []).join(', '))}">${e.host_count}</td><td class="wrap mono">${esc(e.text)}</td></tr>`).join('')}</tbody></table></div>`
      : '<div class="empty">No alerts yet.</div>';
  } catch (err) { $('#al-list').innerHTML = `<div class="msg bad">${esc(err.message)}</div>`; }
}

async function renderSettings() {
  $('#main').innerHTML = `<div class="card"><div class="card-head"><div><h2>Settings</h2></div></div><div class="pane" style="padding-top:0" id="st-body">Loading…</div></div>`;
  let st;
  try { st = (await api('GET', '/api/settings')).data; }
  catch (err) { $('#st-body').innerHTML = `<div class="msg bad">${esc(err.message)}</div>`; return; }
  $('#st-body').innerHTML = `<div class="stack" style="max-width:720px">
    <h3 class="sec" style="margin-top:0">Agent-down alerts</h3>
    <p class="muted" style="margin:0">When an agent stops reporting, the dashboard alerts here (a group can use its own URL instead). It alerts again when the agent is back.</p>
    <label class="field"><span>Ops webhook URL</span><input type="text" id="st-url" value="${esc(st.ops_webhook?.url || '')}" placeholder="https://hooks.slack.com/services/…  or  \${OPS_WEBHOOK_URL}"></label>
    <label class="field"><span>Body template <span class="muted">(optional)</span></span>
      <input type="text" id="st-tmpl" value="${esc(st.ops_webhook?.template || '')}" placeholder='{"text": {{ json .Text }}}'>
      <small>Go template with the alert fields; empty sends the alert as JSON.</small></label>
    <label class="field" style="max-width:260px"><span>Alert when unreachable for</span>
      <select id="st-after">${['30s', '1m', '2m', '5m', '10m', '30m'].map(v => `<option ${v === (st.agent_down_after || '2m') ? 'selected' : ''}>${v}</option>`).join('')}</select></label>
    <div class="row" style="margin:0"><button class="primary" data-action="save-settings">Save</button><button data-action="test-ops">Send a test alert</button></div>
    <div id="st-msg"></div></div>`;
}

// ---------- signed-in user ----------
// The UI hides what the user may not do; the server enforces the same rules
// on every request regardless.
let me = { identity: {}, perms: {} };
const isAdmin = () => !!me.perms.admin;
const rank = { viewer: 1, editor: 2 };
function can(role, group) {
  const p = me.perms;
  if (p.admin) return true;
  if ((rank[p.global] || 0) >= rank[role]) return true;
  return !!group && (rank[(p.groups || {})[group]] || 0) >= rank[role];
}

async function loadMe() {
  me = (await api('GET', '/api/me')).data;
  const id = me.identity, p = me.perms;
  const roleText = p.admin ? 'admin' : [p.global ? `${p.global} (all groups)` : '',
    ...Object.entries(p.groups || {}).map(([g, r]) => `${r} of ${g}`)].filter(Boolean).join(', ') || 'no access';
  $('#usermenu').innerHTML = me.open ? '' : `<div class="who"><b>${esc(id.name || id.user)}</b><small>${esc(roleText)}</small></div>
    <button data-action="logout" title="Sign out">Sign out</button>`;
  $('#open-banner').hidden = !me.open;
  document.querySelectorAll('[data-admin]').forEach(e => e.hidden = !isAdmin());
  if (!me.open && !p.admin && !p.global && !Object.keys(p.groups || {}).length) {
    $('#main').innerHTML = `<div class="card"><div class="empty"><h3>No access yet</h3>
      <p>You are signed in as <b>${esc(id.user)}</b>, but no role has been granted to you.<br>Ask an admin to give you access under <b>Users &amp; access</b>.</p></div></div>`;
    $('#nav').innerHTML = '';
    return false;
  }
  return true;
}

// ---------- users & access (admin) ----------
async function renderAccess() {
  $('#main').innerHTML = `<div class="card"><div class="card-head"><div><h2>Users &amp; access</h2>
      <div class="sub">Roles: <b>viewer</b> sees a group, <b>editor</b> also edits its rules, syncs and silences it,
      <b>admin</b> manages everything (agents, groups, enrollment, settings, users, audit log).
      Grant roles to a user (email for SSO, username for local accounts) or to a group from your identity provider.</div></div></div>
    <div class="pane" style="padding-top:0" id="acc-body">Loading…</div></div>`;
  let users, bindings;
  try { [users, bindings] = await Promise.all([api('GET', '/api/users'), api('GET', '/api/access')]); }
  catch (err) { $('#acc-body').innerHTML = `<div class="msg bad">${esc(err.message)}</div>`; return; }
  const u = users.data, b = bindings.data;
  const groupOpts = `<option value="">All groups</option>` + data.groups.map(g => `<option>${esc(g.name)}</option>`).join('');
  $('#acc-body').innerHTML = `
    <h3 class="sec" style="margin-top:0">Role bindings</h3>
    <div class="row" style="margin:0 0 10px;align-items:flex-end">
      <label class="field" style="width:120px"><span>Who</span><select id="ab-kind"><option value="user">User</option><option value="group">IdP group</option></select></label>
      <label class="field" style="flex:1;min-width:180px"><span>&nbsp;</span><input type="text" id="ab-subject" placeholder="dana@example.com  or  db-team"></label>
      <label class="field" style="width:120px"><span>Role</span><select id="ab-role"><option>viewer</option><option>editor</option><option>admin</option></select></label>
      <label class="field" style="width:170px"><span>On</span><select id="ab-group">${groupOpts}</select></label>
      <button class="primary" data-action="grant">Grant</button>
    </div>
    <div class="scroll"><table><thead><tr><th>Who</th><th>Role</th><th>On</th><th>Granted</th><th></th></tr></thead><tbody>
      ${b.config_admins.map(a => `<tr><td>${esc(a)}</td><td><span class="badge ok">admin</span></td><td>all groups</td><td class="muted">dashboard config file</td><td></td></tr>`).join('')}
      ${b.bindings.map(x => `<tr><td>${esc(x.subject)}</td><td><span class="badge">${esc(x.role)}</span></td><td>${x.group ? esc(x.group) : 'all groups'}</td>
        <td class="muted">${ago(x.created_at)}${x.created_by ? ' by ' + esc(x.created_by) : ''}</td>
        <td><button class="danger" data-action="revoke-binding" data-id="${esc(x.id)}">Revoke</button></td></tr>`).join('')}
    </tbody></table></div>
    <h3 class="sec">Local accounts</h3>
    <p class="muted" style="margin:0 0 10px">For teams without SSO, and for break-glass access. Passwords need at least 12 characters.</p>
    <div class="row" style="margin:0 0 10px;align-items:flex-end">
      <label class="field" style="width:200px"><span>Username</span><input type="text" id="lu-name" autocomplete="off"></label>
      <label class="field" style="width:220px"><span>Password</span><input type="password" id="lu-pass" autocomplete="new-password"></label>
      <button data-action="save-user">Create / reset password</button>
    </div>
    <div class="scroll"><table><thead><tr><th>Username</th><th>Created</th><th></th></tr></thead><tbody>
      ${u.builtin_admin ? `<tr><td>${esc(u.builtin_admin)}</td><td class="muted">built-in admin from the config file</td><td></td></tr>` : ''}
      ${u.users.map(x => `<tr><td>${esc(x.username)}</td><td>${ago(x.created_at)}</td>
        <td><button class="danger" data-action="delete-user" data-name="${esc(x.username)}">Delete</button></td></tr>`).join('')}
    </tbody></table></div>
    <div id="acc-msg"></div>`;
}

// ---------- audit log (admin) ----------
let auditFilter = '';
async function renderAudit() {
  if (!$('#au-list')) $('#main').innerHTML = `<div class="card"><div class="card-head"><div><h2>Audit log</h2>
      <div class="sub">Who changed what, when and from where: sign-ins, agents, groups, rules (with diffs), silences, settings, tokens, users and access, plus refused attempts.</div></div>
      <div class="actions"><input type="text" id="au-q" placeholder="Filter: user, action, group…" value="${esc(auditFilter)}" style="width:240px">
      <a class="button" style="width:auto;padding:6px 12px" href="/api/audit/export">Export (JSON lines)</a></div></div>
    <div id="au-list" class="pane">Loading…</div></div>`;
  try {
    const { data: list } = await api('GET', `/api/audit?limit=500&q=${enc(auditFilter)}`);
    const cls = { ok: 'ok', failed: 'bad', denied: 'warn' };
    $('#au-list').innerHTML = list.length ? `<div class="scroll"><table><thead><tr><th>Time</th><th>Who</th><th>Action</th><th>Target</th><th>Result</th><th>Details</th></tr></thead><tbody>
      ${list.map((e, i) => `<tr><td title="${esc(e.time)}">${new Date(e.time).toLocaleString()}</td>
        <td>${esc(e.actor)}<div class="muted" style="font-size:12px">${esc(e.method || '')}${e.ip ? ' · ' + esc(e.ip) : ''}</div></td>
        <td class="mono">${esc(e.action)}</td><td>${esc(e.target || '')}${e.group && e.group !== e.target ? ` <span class="muted">(${esc(e.group)})</span>` : ''}</td>
        <td><span class="badge ${cls[e.result] || ''}">${esc(e.result)}</span></td>
        <td class="wrap">${esc(e.detail || '')}${e.diff ? `<details><summary class="muted">diff</summary><pre class="diff mono">${diffHTML(e.diff)}</pre></details>` : ''}</td></tr>`).join('')}
      </tbody></table></div>` : '<div class="empty">No entries.</div>';
  } catch (err) { $('#au-list').innerHTML = `<div class="msg bad">${esc(err.message)}</div>`; }
}

const diffHTML = d => d.split('\n').map(l => l.startsWith('+ ') ? `<span class="add">${esc(l)}</span>` : l.startsWith('- ') ? `<span class="del">${esc(l)}</span>` : esc(l)).join('\n');

// ---------- refresh ----------
async function refresh() {
  try {
    const [ov, sil] = await Promise.all([api('GET', '/api/overview'), api('GET', '/api/silences').catch(() => ({ data: [] }))]);
    data = ov.data; silences = sil.data || [];
  }
  catch (e) { $('#summary').textContent = 'dashboard error: ' + e.message; return; }
  // Form pages keep their inputs: once shown, refreshes only update the nav.
  const formPage = ['enroll', 'silences', 'settings', 'access', 'audit'].includes(view.kind) && $('#main').dataset.view === view.kind;
  if (document.querySelector('dialog[open]') || formPage) { renderNav(); return; }
  // Keep focus/scroll and unsaved edits stable: re-render only if not editing.
  const editing = editorDirty || document.activeElement?.matches('textarea, input');
  if (editing) { renderNav(); return; }
  render();
}

// ---------- dialogs ----------
function groupOptions(selected) {
  return `<option value="">No group (own rules)</option>` +
    data.groups.map(g => `<option value="${esc(g.name)}" ${g.name === selected ? 'selected' : ''}>${esc(g.name)}</option>`).join('');
}

function openAgentDialog(agent, group) {
  editingAgent = agent || null;
  const f = $('#f-agent'); f.reset();
  f.group.innerHTML = groupOptions(agent ? agent.group : group);
  $('#fa-msg').innerHTML = '';
  $('#fa-enroll-hint').hidden = !!agent;
  $('#fa-title').textContent = agent ? 'Edit agent ' + agent.name : 'Connect agent';
  $('#fa-save').textContent = agent ? 'Save' : 'Connect';
  f.name.disabled = !!agent;
  f.name.placeholder = 'from the agent'; // reset() does not reset placeholders
  f.token.placeholder = agent && agent.has_token ? 'unchanged (enter a new token to replace it)' : "the agent's token";
  if (agent) { f.name.value = agent.name; f.url.value = agent.url; f.insecure.checked = agent.insecure_skip_verify; }
  delete f.dataset.force;
  $('#dlg-agent').showModal(); (agent ? f.url : f.url).focus();
}

function agentForm() {
  const f = $('#f-agent');
  return { name: editingAgent ? editingAgent.name : f.name.value.trim(), url: f.url.value.trim(), token: f.token.value,
    group: f.group.value, insecure_skip_verify: f.insecure.checked };
}

async function testConnection() {
  const msg = $('#fa-msg');
  msg.innerHTML = '<div class="msg info">Connecting…</div>';
  try {
    const { data: r } = await api('POST', '/api/agents/test', agentForm());
    if (!r.ok) { msg.innerHTML = `<div class="msg bad">Cannot connect: ${esc(r.error)}</div>`; return; }
    const s = r.status;
    msg.innerHTML = `<div class="msg ok">Connected to "${esc(s.name)}" on host ${esc(s.host)} (version ${esc(s.version)}). It runs ${(s.rules || []).length} rules and tails ${(s.files || []).length} files.</div>`;
    const f = $('#f-agent'); if (!editingAgent && !f.name.value) f.name.placeholder = s.name;
  } catch (e) { msg.innerHTML = `<div class="msg bad">${esc(e.message)}</div>`; }
}

async function saveAgent() {
  const f = $('#f-agent'), msg = $('#fa-msg'), body = agentForm();
  if (!body.url) { f.url.focus(); return; }
  const g = body.group && groupOf(body.group);
  if (editingAgent && body.group && body.group !== editingAgent.group && g?.rules_hash &&
      !confirm(`Group "${body.group}" will replace this agent's current rules with the group's rules. Continue?`)) return;
  if (!editingAgent && g?.rules_hash) { /* joining a group with rules: they will be pushed */ }
  body.force = f.dataset.force === '1';
  $('#fa-save').disabled = true;
  try {
    const { data: r } = editingAgent
      ? await api('PUT', `/api/agents/${enc(editingAgent.name)}`, body)
      : await api('POST', '/api/agents', body);
    $('#dlg-agent').close();
    let note = editingAgent ? `Saved ${r.name}` : `Connected ${r.name}`;
    if (r.push) note += r.push.ok ? ` · group rules ${r.push.action}` : ` · pushing group rules failed: ${r.push.error}`;
    toast(note, r.push && !r.push.ok);
    await refresh();
    // Show the agent where it now lives, with its details open.
    go(body.group ? { kind: 'group', name: body.group } : { kind: view.kind === 'ungrouped' ? 'ungrouped' : 'all' });
    selAgent = r.name; agentTab = 'overview'; render();
  } catch (e) {
    if (e.data && e.data.unreachable) {
      msg.innerHTML = `<div class="msg bad">${esc(e.message)}</div><div class="row"><span class="muted">Is the agent not running yet?</span>
        <button type="button" data-action="force-save">Save anyway</button></div>`;
    } else msg.innerHTML = `<div class="msg bad">${esc(e.message)}</div>`;
  } finally { $('#fa-save').disabled = false; }
}

function openGroupDialog(group) {
  editingGroup = group || null;
  const f = $('#f-group'); f.reset();
  $('#fg-msg').innerHTML = '';
  $('#fg-title').textContent = group ? 'Edit group ' + group.name : 'New group';
  $('#fg-save').textContent = group ? 'Save' : 'Create group';
  f.name.disabled = !!group;
  const online = data.agents.filter(a => a.online);
  f.seed.innerHTML = `<option value="example">An example rule set</option><option value="empty">Nothing (empty)</option>` +
    online.map(a => `<option value="agent:${esc(a.name)}">A copy of ${esc(a.name)}'s current rules</option>`).join('');
  if (group) {
    f.name.value = group.name; f.description.value = group.description || ''; f.selector.value = labelsText(group.selector);
    f.fleet_on.checked = !(group.fleet && group.fleet.disabled);
    if (group.fleet && group.fleet.wait) f.fleet_wait.value = group.fleet.wait;
    if (group.fleet && group.fleet.window) f.fleet_window.value = group.fleet.window;
    f.ops_webhook.value = group.ops_webhook_url || '';
    f.source.value = group.source;
    if (group.git) {
      f.provider.value = group.git.provider; f.repo.value = group.git.repo || ''; f.ref.value = group.git.ref || '';
      f.base_url.value = group.git.base_url || ''; f.interval.value = group.git.interval || '';
      if (group.git.provider === 'url') f.rawurl.value = group.git.path; else f.path.value = group.git.path;
      f.gtoken.placeholder = group.git.has_token ? 'unchanged (enter a new token to replace it)' : 'read-only token';
    }
  } else f.gtoken.placeholder = 'read-only token';
  groupFormVisibility();
  $('#dlg-group').showModal(); (group ? f.description : f.name).focus();
}

function groupFormVisibility() {
  const f = $('#f-group'), git = f.source.value === 'git', url = f.provider.value === 'url';
  $('#fg-git').hidden = !git;
  $('#fg-manual').hidden = git || !!editingGroup;
  document.querySelectorAll('#f-group [data-git=repo]').forEach(e => e.hidden = url);
  document.querySelectorAll('#f-group [data-git=url]').forEach(e => e.hidden = !url);
}

async function saveGroup() {
  const f = $('#f-group'), msg = $('#fg-msg');
  const body = { name: f.name.value.trim(), description: f.description.value.trim(), source: f.source.value };
  body.fleet = { disabled: !f.fleet_on.checked, wait: f.fleet_wait.value, window: f.fleet_window.value };
  body.ops_webhook_url = f.ops_webhook.value.trim();
  try { body.selector = parseLabels(f.selector.value); }
  catch (e) { msg.innerHTML = `<div class="msg bad">Labels: ${esc(e.message)}</div>`; f.selector.focus(); return; }
  if (!body.name) { f.name.focus(); return; }
  if (body.source === 'git') {
    const url = f.provider.value === 'url';
    body.git = { provider: f.provider.value, repo: url ? '' : f.repo.value.trim(), ref: url ? '' : f.ref.value.trim(),
      path: url ? f.rawurl.value.trim() : f.path.value.trim(), base_url: url ? '' : f.base_url.value.trim(),
      token: f.gtoken.value, interval: f.interval.value };
  } else if (!editingGroup) {
    const seed = f.seed.value;
    if (seed === 'example') body.rules = EXAMPLE_RULES;
    else if (seed.startsWith('agent:')) {
      try { body.rules = (await api('GET', `/api/agents/${enc(seed.slice(6))}/rules`)).data; }
      catch (e) { msg.innerHTML = `<div class="msg bad">Could not copy rules: ${esc(e.message)}</div>`; return; }
    }
  }
  $('#fg-save').disabled = true;
  msg.innerHTML = body.source === 'git' ? '<div class="msg info">Saving and fetching from git…</div>' : '';
  try {
    const { data: r } = editingGroup
      ? await api('PUT', `/api/groups/${enc(editingGroup.name)}`, body)
      : await api('POST', '/api/groups', body);
    $('#dlg-group').close();
    if (r.sync_error) toast(`Group saved, but syncing from git failed: ${r.sync_error}`, true);
    else toast(editingGroup ? `Saved group ${r.name}` : `Created group ${r.name}`);
    await refresh();
    go({ kind: 'group', name: r.name });
  } catch (e) { msg.innerHTML = `<div class="msg bad">${esc(e.message)}</div>`; }
  finally { $('#fg-save').disabled = false; }
}

function addExisting(group) {
  const candidates = data.agents.filter(a => a.group !== group);
  const dlg = document.createElement('dialog');
  dlg.innerHTML = `<form method="dialog"><h2>Add agents to ${esc(group)}</h2>
    <p class="muted" style="margin:0">The group's rules replace each agent's current rules.</p>
    <div style="display:grid;gap:6px;max-height:300px;overflow:auto">${candidates.map(a => `<label class="check"><input type="checkbox" value="${esc(a.name)}">
      <b>${esc(a.name)}</b> <span class="muted">${a.group ? 'in ' + esc(a.group) : 'ungrouped'} · ${esc(a.url || a.hostname || '')}</span></label>`).join('')}</div>
    <div class="foot"><button value="cancel">Cancel</button><button class="primary" value="ok">Add to group</button></div></form>`;
  document.body.append(dlg); dlg.showModal();
  dlg.addEventListener('close', async () => {
    const names = [...dlg.querySelectorAll('input:checked')].map(i => i.value);
    const ok = dlg.returnValue === 'ok'; dlg.remove();
    if (!ok || !names.length) return;
    for (const n of names) {
      const a = agentOf(n);
      try {
        const { data: r } = await api('PUT', `/api/agents/${enc(n)}`, { url: a.url, group, insecure_skip_verify: a.insecure_skip_verify });
        toast(`${n} added to ${group}` + (r.push ? (r.push.ok ? ` · rules ${r.push.action}` : ` · push failed: ${r.push.error}`) : ''), r.push && !r.push.ok);
      } catch (e) { toast(`${n}: ${e.message}`, true); }
    }
    refresh();
  });
}

// ---------- events ----------
document.addEventListener('click', async e => {
  const t = e.target.closest('[data-action],[data-nav],[data-agent],[data-atab],[data-gtab]');
  if (!t) return;
  if (t.dataset.nav) { e.preventDefault(); return go(t.dataset.nav === 'group' ? { kind: 'group', name: t.dataset.name } : { kind: t.dataset.nav }); }
  if (t.dataset.agent && !t.dataset.action) { selAgent = t.dataset.agent; agentTab = 'overview'; editorDirty = false; render();
    $('#agent-detail')?.scrollIntoView({ behavior: 'smooth', block: 'nearest' }); return; }
  if (t.dataset.atab) { agentTab = t.dataset.atab; editorDirty = false; return renderAgentDetail(); }
  if (t.dataset.gtab) { groupTab = t.dataset.gtab; editorDirty = false; return renderMain(); }

  const g = view.kind === 'group' && groupOf(view.name);
  const a = selAgent && agentOf(selAgent);
  switch (t.dataset.action) {
    case 'connect': return openAgentDialog(null, t.dataset.group || (view.kind === 'group' ? view.name : ''));
    case 'new-group': return openGroupDialog();
    case 'close': return t.closest('dialog').close();
    case 'test-connection': return testConnection();
    case 'save-agent': return saveAgent();
    case 'force-save': $('#f-agent').dataset.force = '1'; return saveAgent();
    case 'save-group': return saveGroup();
    case 'add-existing': return addExisting(t.dataset.group);
    case 'close-agent': selAgent = null; return render();
    case 'edit-agent': return openAgentDialog(a);
    case 'goto-enroll': $('#dlg-agent').close(); return go({ kind: 'enroll' });
    case 'logout': {
      // SSO users are sent on to the identity provider to end that session too.
      let to = '/login';
      try { const { data: r } = await api('POST', '/auth/logout'); if (r && r.redirect) to = r.redirect; } catch {}
      location.href = to; return;
    }
    case 'grant': {
      const value = $('#ab-subject').value.trim();
      if (!value) { $('#ab-subject').focus(); return; }
      try {
        await api('POST', '/api/access', { subject: `${$('#ab-kind').value}:${value}`, role: $('#ab-role').value, group: $('#ab-role').value === 'admin' ? '' : $('#ab-group').value });
        toast('Access granted'); renderAccess();
      } catch (err) { toast(err.message, true); }
      return;
    }
    case 'revoke-binding':
      if (!confirm('Revoke this role?')) return;
      try { await api('DELETE', `/api/access/${enc(t.dataset.id)}`); toast('Access revoked'); renderAccess(); }
      catch (err) { toast(err.message, true); } return;
    case 'save-user': {
      const name = $('#lu-name').value.trim(), pw = $('#lu-pass').value;
      if (!name) { $('#lu-name').focus(); return; }
      try {
        const { data: r } = await api('PUT', `/api/users/${enc(name)}`, { password: pw });
        toast(r.created ? `Created ${name}. Grant a role to user:${name} so it can see something.` : `Password of ${name} reset; their sessions were ended`);
        renderAccess();
      } catch (err) { toast(err.message, true); }
      return;
    }
    case 'delete-user':
      if (!confirm(`Delete local user ${t.dataset.name}? Their sessions end immediately.`)) return;
      try { await api('DELETE', `/api/users/${enc(t.dataset.name)}`); toast('User deleted'); renderAccess(); }
      catch (err) { toast(err.message, true); } return;
    case 'silence-group': go({ kind: 'silences' }); $('#sl-group').value = g.name; $('#sl-reason').focus(); return;
    case 'create-silence': {
      const split = s => s.split(',').map(x => x.trim()).filter(Boolean);
      const body = { group: $('#sl-group').value, duration: $('#sl-dur').value, rules: split($('#sl-rules').value),
        agents: split($('#sl-agents').value), reason: $('#sl-reason').value.trim() };
      try { await api('POST', '/api/silences', body); toast('Silence created'); silences = (await api('GET', '/api/silences')).data; renderSilences(); renderNav(); }
      catch (err) { toast(err.message, true); }
      return;
    }
    case 'end-silence':
      try { await api('DELETE', `/api/silences/${enc(t.dataset.id)}`); toast('Silence ended'); await refresh(); render(); }
      catch (err) { toast(err.message, true); } return;
    case 'save-settings':
      try {
        await api('PUT', '/api/settings', { ops_webhook: { url: $('#st-url').value.trim(), template: $('#st-tmpl').value.trim() }, agent_down_after: $('#st-after').value });
        $('#st-msg').innerHTML = '<div class="msg ok">Saved.</div>';
      } catch (err) { $('#st-msg').innerHTML = `<div class="msg bad">${esc(err.message)}</div>`; }
      return;
    case 'test-ops':
      try { const { data: r } = await api('POST', '/api/settings/test-ops'); $('#st-msg').innerHTML = `<div class="msg ok">Test alert sent to ${esc(r.sent)}.</div>`; }
      catch (err) { $('#st-msg').innerHTML = `<div class="msg bad">Test failed: ${esc(err.message)} (save the settings first)</div>`; }
      return;
    case 'create-token': {
      const name = $('#tok-name').value.trim();
      if (!name) { $('#tok-name').focus(); return; }
      try {
        const { data: r } = await api('POST', '/api/tokens', { name, ttl: $('#tok-ttl').value });
        newToken = r; renderEnroll();
      } catch (err) { toast(err.message, true); }
      return;
    }
    case 'revoke-token':
      if (!confirm('Revoke this token? New agents can no longer enroll with it. Agents that already enrolled keep working.')) return;
      try { await api('DELETE', `/api/tokens/${enc(t.dataset.id)}`); toast('Token revoked'); loadTokens(); }
      catch (err) { toast(err.message, true); } return;
    case 'copy':
      try { await navigator.clipboard.writeText(t.dataset.text); toast('Copied'); }
      catch { toast('Copy failed: select the text and copy it manually', true); } return;
    case 'save-agent-group': {
      const group = $('#a-group').value, target = group || null;
      if (target && target !== a.group && groupOf(target)?.rules_hash &&
          !confirm(`Group "${target}" will replace this agent's current rules with the group's rules. Continue?`)) return;
      try {
        const { data: r } = await api('PUT', `/api/agents/${enc(a.name)}`, { group });
        toast(`Saved ${a.name}` + (r.push ? (r.push.ok ? ` · group rules ${r.push.action}` : ` · push failed: ${r.push.error}`) : ''), r.push && !r.push.ok);
        await refresh(); agentTab = 'settings'; render();
      } catch (err) { toast(err.message, true); }
      return;
    }
    case 'revoke-agent':
      if (!confirm(`Revoke ${a.name}? The dashboard will refuse its connection and stop managing it. The agent keeps running with its current rules.`)) return;
      try { await api('POST', `/api/agents/${enc(a.name)}/revoke`); toast(`Revoked ${a.name}`); await refresh(); agentTab = 'settings'; render(); }
      catch (err) { toast(err.message, true); } return;
    case 'allow-agent':
      try { await api('POST', `/api/agents/${enc(a.name)}/allow`); toast(`${a.name} allowed again; it reconnects within 15 seconds`); await refresh(); agentTab = 'settings'; render(); }
      catch (err) { toast(err.message, true); } return;
    case 'remove-agent':
      if (!confirm(`Remove ${a.name} from the dashboard? The agent keeps running with its current rules.`)) return;
      try { await api('DELETE', `/api/agents/${enc(a.name)}`); toast(`Removed ${a.name}`); selAgent = null; refresh(); }
      catch (err) { toast(err.message, true); } return;
    case 'edit-group': return openGroupDialog(g);
    case 'delete-group':
      if (!confirm(`Delete group ${g.name}? Its ${g.members.length} agent(s) become ungrouped and keep their current rules.`)) return;
      try { await api('DELETE', `/api/groups/${enc(g.name)}`); toast(`Deleted group ${g.name}`); await refresh(); go({ kind: 'all' }); }
      catch (err) { toast(err.message, true); } return;
    case 'sync-group':
      t.disabled = true; t.textContent = 'Syncing…';
      try { const { data: st } = await api('POST', `/api/groups/${enc(g.name)}/sync`); toast('Synced: ' + pushSummary(st).replace(/\n/g, ' · ')); }
      catch (err) { toast(err.message, true); }
      editorDirty = false; return refresh();
    case 'validate-group':
      try { const { data: r } = await api('POST', `/api/groups/${enc(g.name)}/validate`, $('#g-editor').value, 'application/yaml');
        $('#g-msg').innerHTML = `<div class="msg ok">Valid: ${r.rules} active rules.</div>`; }
      catch (err) { $('#g-msg').innerHTML = `<div class="msg bad">${esc(err.message)}</div>`; } return;
    case 'save-group-rules':
      try { const { data: r } = await api('PUT', `/api/groups/${enc(g.name)}/rules`, $('#g-editor').value, 'application/yaml');
        editorDirty = false;
        await refresh(); // re-renders the tab, so show the result after it
        $('#g-msg').innerHTML = `<div class="msg ok">Saved version ${esc(r.hash)} (${r.rules} active rules), applied without restarting:\n${esc(pushSummary(r.state))}</div>`; }
      catch (err) { $('#g-msg').innerHTML = `<div class="msg bad">${esc(err.message)}</div>`; } return;
    case 'reload-group-rules': $('#g-msg').innerHTML = ''; return loadGroupRules(true);
    case 'preview-git':
      try { const { data: text, headers } = await api('GET', `/api/groups/${enc(g.name)}/preview`);
        const same = headers.get('X-Rules-Hash') === g.rules_hash;
        $('#g-preview').innerHTML = `<h3 class="sec">Latest in git (version ${esc(headers.get('X-Rules-Hash'))}${same ? ', same as applied' : ', <b>not applied yet</b>'})</h3><pre class="preview mono">${esc(text)}</pre>`; }
      catch (err) { $('#g-preview').innerHTML = `<div class="msg bad">${esc(err.message)}</div>`; } return;
    case 'validate-agent':
      try { const { data: r } = await api('POST', `/api/agents/${enc(a.name)}/validate`, $('#a-editor').value, 'application/yaml');
        $('#a-msg').innerHTML = `<div class="msg ok">Valid: ${r.rules} active rules.</div>`; }
      catch (err) { $('#a-msg').innerHTML = `<div class="msg bad">${esc(err.message)}</div>`; } return;
    case 'save-agent-rules':
      try { const { data: r } = await api('PUT', `/api/agents/${enc(a.name)}/rules`, $('#a-editor').value, 'application/yaml');
        editorDirty = false;
        await refresh(); // re-renders the tab, so show the result after it
        $('#a-msg').innerHTML = `<div class="msg ok">${r.unchanged ? 'No changes.' : `Applied without restarting: ${r.rules} active rules.`}</div>`; }
      catch (err) { $('#a-msg').innerHTML = `<div class="msg bad">${esc(err.message)}</div>`; } return;
    case 'reload-agent-rules': $('#a-msg').innerHTML = ''; return loadAgentRules(true);
    case 'run-test':
      try { const { data: r } = await api('POST', `/api/agents/${enc(a.name)}/test`, { line: $('#t-line').value });
        $('#t-msg').innerHTML = r.matched && r.matched.length ? `<div class="msg ok">Matches: ${r.matched.map(esc).join(', ')}</div>` : '<div class="msg">No rule matches this line.</div>'; }
      catch (err) { $('#t-msg').innerHTML = `<div class="msg bad">${esc(err.message)}</div>`; } return;
  }
});
document.addEventListener('input', e => { if (e.target.matches('#g-editor, #a-editor')) editorDirty = true; });
document.addEventListener('keydown', e => {
  if (e.key === 'Tab' && e.target.matches('textarea:not([readonly])')) {
    e.preventDefault(); const t = e.target; t.setRangeText('  ', t.selectionStart, t.selectionEnd, 'end'); editorDirty = true;
  }
  if (e.key === 'Enter' && e.target.id === 't-line') $('[data-action=run-test]').click();
});
let auditTimer;
document.addEventListener('input', e => {
  if (e.target.id === 'au-q') { clearTimeout(auditTimer); auditTimer = setTimeout(() => { auditFilter = e.target.value; renderAudit(); }, 300); }
});
document.addEventListener('change', e => { if (e.target.id === 'al-group') { alertsGroup = e.target.value; renderAlerts(); } });
$('#f-group').addEventListener('change', e => { if (e.target.name === 'source' || e.target.name === 'provider') groupFormVisibility(); });
window.addEventListener('hashchange', () => { view = parseHash(); render(); });

loadMe().then(ok => {
  if (!ok) return;
  refresh();
  setInterval(() => { if ($('#auto').checked && !document.hidden) refresh(); }, 5000);
}).catch(() => {});
