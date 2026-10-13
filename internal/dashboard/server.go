// Package dashboard implements the grepalert web dashboard: connect agents,
// organise them into groups that share a rule set (edited here or synced from
// GitHub/GitLab), and monitor them.
package dashboard

import (
	"context"
	"crypto/subtle"
	"crypto/tls"
	"embed"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"log/slog"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/fadhlanhawali/grepalert/internal/rules"
)

//go:embed web
var webFS embed.FS

const maxProxyBody = 2 << 20

type Server struct {
	cfg   Config
	log   *slog.Logger
	store *Store
	git   *http.Client

	clMu    sync.Mutex
	clients map[string]*agentClient // keyed by agent name

	stMu        sync.Mutex
	groupStates map[string]*groupState

	tunMu   sync.Mutex
	tunnels map[string]*tunnel // keyed by connected agent ID

	fleet *fleet // fleet dedupe, silences, agent-down alerts, history

	stopping     chan struct{} // closed by Shutdown to release held polls
	stoppingOnce sync.Once
}

type agentClient struct {
	rec  AgentRecord
	http *http.Client
}

func New(cfg Config, store *Store, log *slog.Logger) *Server {
	cfg.setDefaults()
	s := &Server{
		cfg:         cfg,
		log:         log,
		store:       store,
		git:         &http.Client{Timeout: 30 * time.Second},
		clients:     map[string]*agentClient{},
		groupStates: map[string]*groupState{},
		tunnels:     map[string]*tunnel{},
		stopping:    make(chan struct{}),
	}
	s.fleet = newFleet(s)
	return s
}

// RunFleet delivers fleet alerts and watches for agents going down, until ctx
// is done.
func (s *Server) RunFleet(ctx context.Context) { s.fleet.Run(ctx) }

// tunnelBase is the placeholder URL for connected agents; requests to it are
// carried by tunnelTransport, never dialed.
const tunnelBase = "http://agent.tunnel"

// client returns a cached HTTP client for the agent, rebuilt when its
// connection settings change. Connected agents are reached through their
// poll tunnel, direct agents over HTTP(S).
func (s *Server) client(a AgentRecord) *agentClient {
	s.clMu.Lock()
	defer s.clMu.Unlock()
	if c, ok := s.clients[a.Name]; ok && c.rec.URL == a.URL && c.rec.Token == a.Token &&
		c.rec.InsecureSkipVerify == a.InsecureSkipVerify && c.rec.ID == a.ID && c.rec.Mode == a.Mode {
		return c
	}
	var c *agentClient
	if a.Connected() {
		rec := a
		rec.URL, rec.Token = tunnelBase, ""
		c = &agentClient{rec: rec, http: &http.Client{Transport: &tunnelTransport{s: s, id: a.ID}, Timeout: 15 * time.Second}}
	} else {
		c = newAgentClient(a)
	}
	s.clients[a.Name] = c
	return c
}

func newAgentClient(a AgentRecord) *agentClient {
	tr := http.DefaultTransport.(*http.Transport).Clone()
	tr.MaxIdleConnsPerHost = 2
	if a.InsecureSkipVerify {
		tr.TLSClientConfig = &tls.Config{InsecureSkipVerify: true}
	}
	return &agentClient{rec: a, http: &http.Client{Transport: tr, Timeout: 15 * time.Second}}
}

// call performs a request against an agent's API.
func (c *agentClient) call(ctx context.Context, method, path string, body io.Reader, hdr http.Header) (*http.Response, error) {
	req, err := http.NewRequestWithContext(ctx, method, strings.TrimSuffix(c.rec.URL, "/")+path, body)
	if err != nil {
		return nil, err
	}
	for k, v := range hdr {
		req.Header[k] = v
	}
	if tok := rules.ExpandEnv(c.rec.Token); tok != "" {
		req.Header.Set("Authorization", "Bearer "+tok)
	}
	return c.http.Do(req)
}

// status fetches an agent's status JSON.
func (c *agentClient) status(ctx context.Context) (json.RawMessage, error) {
	resp, err := c.call(ctx, http.MethodGet, "/api/v1/status", nil, nil)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(io.LimitReader(resp.Body, maxProxyBody))
	switch {
	case resp.StatusCode == http.StatusUnauthorized:
		return nil, errors.New("the agent rejected the token (HTTP 401)")
	case resp.StatusCode != http.StatusOK:
		return nil, fmt.Errorf("HTTP %d: %s", resp.StatusCode, strings.TrimSpace(string(body)))
	case !json.Valid(body):
		return nil, errors.New("the URL answered, but not like a grepalert agent")
	}
	return body, nil
}

func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()
	static, _ := fs.Sub(webFS, "web")
	mux.Handle("GET /", http.FileServerFS(static))

	mux.HandleFunc("GET /api/overview", s.handleOverview)

	mux.HandleFunc("POST /api/agents/test", s.handleTestConnection)
	mux.HandleFunc("POST /api/agents", s.handleAddAgent)
	mux.HandleFunc("PUT /api/agents/{name}", s.handleUpdateAgent)
	mux.HandleFunc("DELETE /api/agents/{name}", s.handleDeleteAgent)
	mux.HandleFunc("GET /api/agents/{name}/status", s.proxy("GET", "/api/v1/status"))
	mux.HandleFunc("GET /api/agents/{name}/rules", s.proxy("GET", "/api/v1/rules"))
	mux.HandleFunc("PUT /api/agents/{name}/rules", s.proxy("PUT", "/api/v1/rules"))
	mux.HandleFunc("POST /api/agents/{name}/validate", s.proxy("POST", "/api/v1/rules/validate"))
	mux.HandleFunc("GET /api/agents/{name}/events", s.proxy("GET", "/api/v1/events"))
	mux.HandleFunc("POST /api/agents/{name}/test", s.proxy("POST", "/api/v1/test"))

	mux.HandleFunc("POST /api/groups", s.handleAddGroup)
	mux.HandleFunc("PUT /api/groups/{name}", s.handleUpdateGroup)
	mux.HandleFunc("DELETE /api/groups/{name}", s.handleDeleteGroup)
	mux.HandleFunc("GET /api/groups/{name}/rules", s.handleGroupRules)
	mux.HandleFunc("PUT /api/groups/{name}/rules", s.handleSetGroupRules)
	mux.HandleFunc("POST /api/groups/{name}/validate", s.handleValidate)
	mux.HandleFunc("POST /api/groups/{name}/sync", s.handleSyncGroup)
	mux.HandleFunc("GET /api/groups/{name}/preview", s.handlePreview)

	mux.HandleFunc("GET /api/tokens", s.handleListTokens)
	mux.HandleFunc("POST /api/tokens", s.handleCreateToken)
	mux.HandleFunc("DELETE /api/tokens/{id}", s.handleRevokeToken)
	mux.HandleFunc("POST /api/agents/{name}/revoke", s.handleSetRevoked(true))
	mux.HandleFunc("POST /api/agents/{name}/allow", s.handleSetRevoked(false))

	mux.HandleFunc("GET /api/alerts", s.handleHistory)
	mux.HandleFunc("GET /api/silences", s.handleListSilences)
	mux.HandleFunc("POST /api/silences", s.handleAddSilence)
	mux.HandleFunc("DELETE /api/silences/{id}", s.handleExpireSilence)
	mux.HandleFunc("GET /api/settings", s.handleGetSettings)
	mux.HandleFunc("PUT /api/settings", s.handlePutSettings)
	mux.HandleFunc("POST /api/settings/test-ops", s.handleTestOps)

	// Endpoints for connected agents. They authenticate with enrollment
	// tokens or agent credentials, not with the dashboard login.
	mux.HandleFunc("POST /agent/v1/enroll", s.handleEnroll)
	mux.HandleFunc("POST /agent/v1/poll", s.handlePoll)
	mux.HandleFunc("POST /agent/v1/respond", s.handleRespond)
	mux.HandleFunc("POST /agent/v1/alerts", s.handleAlerts)
	return s.auth(mux)
}

func (s *Server) auth(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasPrefix(r.URL.Path, "/agent/v1/") {
			next.ServeHTTP(w, r)
			return
		}
		if pw := s.cfg.Auth.Password; pw != "" {
			u, p, ok := r.BasicAuth()
			if !ok || subtle.ConstantTimeCompare([]byte(u), []byte(s.cfg.Auth.Username)) != 1 ||
				subtle.ConstantTimeCompare([]byte(p), []byte(pw)) != 1 {
				w.Header().Set("WWW-Authenticate", `Basic realm="grepalert"`)
				http.Error(w, "unauthorized", http.StatusUnauthorized)
				return
			}
		}
		// CSRF guard: browsers cannot send this header cross-site without a
		// CORS preflight, which we never approve.
		if r.Method != http.MethodGet && r.Header.Get("X-Requested-With") != "grepalert" {
			http.Error(w, "missing X-Requested-With header", http.StatusForbidden)
			return
		}
		w.Header().Set("X-Content-Type-Options", "nosniff")
		w.Header().Set("X-Frame-Options", "DENY")
		next.ServeHTTP(w, r)
	})
}

func readJSON(w http.ResponseWriter, r *http.Request, v any) bool {
	if err := json.NewDecoder(io.LimitReader(r.Body, maxProxyBody)).Decode(v); err != nil {
		writeErr(w, http.StatusBadRequest, fmt.Errorf("invalid request: %w", err))
		return false
	}
	return true
}

// ---- overview ----

type agentView struct {
	Name string `json:"name"`
	Mode string `json:"mode"` // "direct" or "connected"
	URL  string `json:"url,omitempty"`
	// Group is the effective group; Assigned tells whether an admin chose it
	// (otherwise it came from a label selector).
	Group              string            `json:"group,omitempty"`
	Assigned           bool              `json:"assigned,omitempty"`
	HasToken           bool              `json:"has_token"`
	InsecureSkipVerify bool              `json:"insecure_skip_verify,omitempty"`
	AddedAt            time.Time         `json:"added_at"`
	Labels             map[string]string `json:"labels,omitempty"`
	Hostname           string            `json:"hostname,omitempty"`
	EnrolledVia        string            `json:"enrolled_via,omitempty"`
	Revoked            bool              `json:"revoked,omitempty"`
	LastSeen           time.Time         `json:"last_seen,omitempty"`
	DownSince          time.Time         `json:"down_since,omitempty"` // set once an agent-down alert went out
	Online             bool              `json:"online"`
	Error              string            `json:"error,omitempty"`
	Status             json.RawMessage   `json:"status,omitempty"`
}

type gitView struct {
	Provider string `json:"provider"`
	BaseURL  string `json:"base_url,omitempty"`
	Repo     string `json:"repo,omitempty"`
	Ref      string `json:"ref,omitempty"`
	Path     string `json:"path"`
	Interval string `json:"interval,omitempty"`
	HasToken bool   `json:"has_token"`
}

type groupView struct {
	Name           string            `json:"name"`
	Description    string            `json:"description,omitempty"`
	Source         string            `json:"source"`
	Git            *gitView          `json:"git,omitempty"`
	Selector       map[string]string `json:"selector,omitempty"`
	Fleet          FleetSettings     `json:"fleet"`
	OpsWebhookURL  string            `json:"ops_webhook_url,omitempty"`
	RulesHash      string            `json:"rules_hash,omitempty"`
	RulesUpdatedAt time.Time         `json:"rules_updated_at"`
	Members        []string          `json:"members"`
	State          groupState        `json:"state"`
}

func (s *Server) groupView(g GroupRecord) groupView {
	v := groupView{Name: g.Name, Description: g.Description, Source: g.Source, Selector: g.Selector, Fleet: g.Fleet, OpsWebhookURL: g.OpsWebhookURL, RulesHash: g.RulesHash,
		RulesUpdatedAt: g.RulesUpdatedAt, Members: []string{}, State: s.snapshotState(g.Name)}
	if g.Git != nil {
		v.Git = &gitView{Provider: g.Git.Provider, BaseURL: g.Git.BaseURL, Repo: g.Git.Repo, Ref: g.Git.Ref,
			Path: g.Git.Path, Interval: g.Git.Interval, HasToken: g.Git.Token != ""}
	}
	for _, a := range s.store.Members(g.Name) {
		v.Members = append(v.Members, a.Name)
	}
	return v
}

func (s *Server) handleOverview(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(r.Context(), 4*time.Second)
	defer cancel()

	recs, groups, down := s.store.Agents(), s.store.Groups(), s.fleet.downStates()
	out := struct {
		Agents []agentView `json:"agents"`
		Groups []groupView `json:"groups"`
	}{Agents: make([]agentView, len(recs)), Groups: []groupView{}}

	var wg sync.WaitGroup
	for i, a := range recs {
		wg.Add(1)
		go func(i int, a AgentRecord) {
			defer wg.Done()
			v := agentView{Name: a.Name, Mode: "direct", URL: a.URL, Group: EffectiveGroup(a, groups), Assigned: a.Group != "",
				HasToken: a.Token != "", InsecureSkipVerify: a.InsecureSkipVerify, AddedAt: a.AddedAt}
			v.DownSince = down[a.Name]
			if a.Connected() {
				v.Mode, v.URL, v.Labels, v.Hostname, v.EnrolledVia = "connected", "", a.Labels, a.Hostname, a.EnrolledVia
				v.Revoked, v.LastSeen = a.Revoked, s.lastSeenAt(a.ID)
				if a.Revoked {
					v.Group, v.Error = "", "access revoked"
					out.Agents[i] = v
					return
				}
			}
			st, err := s.client(a).status(ctx)
			if err != nil {
				v.Error = err.Error()
			} else {
				v.Online, v.Status = true, st
			}
			out.Agents[i] = v
		}(i, a)
	}
	wg.Wait()
	for _, g := range s.store.Groups() {
		out.Groups = append(out.Groups, s.groupView(g))
	}
	writeJSON(w, http.StatusOK, out)
}

// ---- agents ----

type agentRequest struct {
	Name               string `json:"name"`
	URL                string `json:"url"`
	Token              string `json:"token"`
	Group              string `json:"group"`
	InsecureSkipVerify bool   `json:"insecure_skip_verify"`
	// Force saves the agent even if it cannot be reached right now.
	Force bool `json:"force"`
}

func (req *agentRequest) normalize() error {
	req.Name = strings.TrimSpace(req.Name)
	req.URL = strings.TrimSuffix(strings.TrimSpace(req.URL), "/")
	if req.URL == "" {
		return errors.New("agent URL is required")
	}
	if !strings.HasPrefix(req.URL, "http://") && !strings.HasPrefix(req.URL, "https://") {
		req.URL = "http://" + req.URL
	}
	return nil
}

// probe checks that the agent answers, returning its reported name.
func probe(ctx context.Context, rec AgentRecord) (string, json.RawMessage, error) {
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	st, err := newAgentClient(rec).status(ctx)
	if err != nil {
		return "", nil, err
	}
	var info struct {
		Name string `json:"name"`
	}
	json.Unmarshal(st, &info)
	return info.Name, st, nil
}

func (s *Server) handleTestConnection(w http.ResponseWriter, r *http.Request) {
	var req agentRequest
	if !readJSON(w, r, &req) {
		return
	}
	if err := req.normalize(); err != nil {
		writeErr(w, http.StatusBadRequest, err)
		return
	}
	rec := AgentRecord{URL: req.URL, Token: req.Token, InsecureSkipVerify: req.InsecureSkipVerify}
	if req.Token == "" && req.Name != "" { // editing: reuse the stored token
		if old, ok := s.store.Agent(req.Name); ok {
			rec.Token = old.Token
		}
	}
	_, st, err := probe(r.Context(), rec)
	if err != nil {
		writeJSON(w, http.StatusOK, map[string]any{"ok": false, "error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "status": st})
}

func (s *Server) handleAddAgent(w http.ResponseWriter, r *http.Request) {
	var req agentRequest
	if !readJSON(w, r, &req) {
		return
	}
	if err := req.normalize(); err != nil {
		writeErr(w, http.StatusBadRequest, err)
		return
	}
	rec := AgentRecord{Name: req.Name, URL: req.URL, Token: req.Token, Group: req.Group, InsecureSkipVerify: req.InsecureSkipVerify}
	reported, _, err := probe(r.Context(), rec)
	if err != nil && !req.Force {
		writeJSON(w, http.StatusUnprocessableEntity, map[string]any{"error": "cannot connect: " + err.Error(), "unreachable": true})
		return
	}
	if rec.Name == "" {
		rec.Name = reported
	}
	if rec.Name == "" {
		writeErr(w, http.StatusBadRequest, errors.New("enter a name (the agent could not be asked for one)"))
		return
	}
	if err := s.store.AddAgent(rec); err != nil {
		writeErr(w, http.StatusConflict, err)
		return
	}
	s.log.Info("agent added", "agent", rec.Name, "url", rec.URL, "group", rec.Group)
	writeJSON(w, http.StatusOK, map[string]any{"name": rec.Name, "push": s.pushToNewMember(r.Context(), rec)})
}

func (s *Server) handleUpdateAgent(w http.ResponseWriter, r *http.Request) {
	old, ok := s.store.Agent(r.PathValue("name"))
	if !ok {
		writeErr(w, http.StatusNotFound, errors.New("agent not found"))
		return
	}
	var req agentRequest
	if !readJSON(w, r, &req) {
		return
	}
	if !old.Connected() {
		if err := req.normalize(); err != nil {
			writeErr(w, http.StatusBadRequest, err)
			return
		}
	}
	groups := s.store.Groups()
	rec, err := s.store.UpdateAgent(old.Name, func(a *AgentRecord) error {
		a.Group = req.Group // for connected agents "" means "by labels"
		if !a.Connected() {
			a.URL, a.InsecureSkipVerify = req.URL, req.InsecureSkipVerify
			if req.Token != "" { // empty keeps the stored token
				a.Token = req.Token
			}
		}
		return nil
	})
	if err != nil {
		writeErr(w, http.StatusBadRequest, err)
		return
	}
	var push *PushResult
	if g := EffectiveGroup(rec, groups); g != "" && (g != EffectiveGroup(old, groups) || rec.URL != old.URL) {
		push = s.pushToNewMember(r.Context(), rec)
	}
	writeJSON(w, http.StatusOK, map[string]any{"name": rec.Name, "push": push})
}

// handleSetRevoked blocks (or re-allows) a connected agent. A revoked agent's
// polls are refused, so it gets no rules or requests until allowed again.
func (s *Server) handleSetRevoked(revoked bool) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		rec, err := s.store.UpdateAgent(r.PathValue("name"), func(a *AgentRecord) error {
			if !a.Connected() {
				return errors.New("only agents that connect to the dashboard can be revoked; remove direct agents instead")
			}
			a.Revoked = revoked
			return nil
		})
		if err != nil {
			writeErr(w, http.StatusBadRequest, err)
			return
		}
		s.log.Info("agent access changed", "agent", rec.Name, "revoked", revoked)
		if revoked {
			s.wakeTunnel(rec.ID)
		}
		writeJSON(w, http.StatusOK, map[string]any{"name": rec.Name, "revoked": revoked})
	}
}

func (s *Server) handleListTokens(w http.ResponseWriter, r *http.Request) {
	type tokenView struct {
		EnrollmentToken
		Hash    string `json:"hash,omitempty"` // never sent
		Expired bool   `json:"expired"`
	}
	out := []tokenView{}
	for _, t := range s.store.Tokens() {
		out = append(out, tokenView{EnrollmentToken: t, Expired: !t.ExpiresAt.IsZero() && time.Now().After(t.ExpiresAt)})
	}
	writeJSON(w, http.StatusOK, map[string]any{"tokens": out, "config_token": s.cfg.EnrollmentToken != ""})
}

func (s *Server) handleCreateToken(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Name string `json:"name"`
		TTL  string `json:"ttl"` // e.g. "24h"; empty = never expires
	}
	if !readJSON(w, r, &req) {
		return
	}
	var ttl time.Duration
	if req.TTL != "" {
		d, err := time.ParseDuration(req.TTL)
		if err != nil || d <= 0 {
			writeErr(w, http.StatusBadRequest, errors.New("invalid expiry"))
			return
		}
		ttl = d
	}
	t, raw, err := s.store.CreateToken(strings.TrimSpace(req.Name), ttl)
	if err != nil {
		writeErr(w, http.StatusBadRequest, err)
		return
	}
	s.log.Info("enrollment token created", "name", t.Name, "expires", t.ExpiresAt)
	writeJSON(w, http.StatusOK, map[string]any{"id": t.ID, "name": t.Name, "token": raw, "expires_at": t.ExpiresAt})
}

func (s *Server) handleRevokeToken(w http.ResponseWriter, r *http.Request) {
	if err := s.store.RevokeToken(r.PathValue("id")); err != nil {
		writeErr(w, http.StatusNotFound, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"revoked": r.PathValue("id")})
}

func (s *Server) handleDeleteAgent(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("name")
	if err := s.store.DeleteAgent(name); err != nil {
		writeErr(w, http.StatusNotFound, err)
		return
	}
	s.clMu.Lock()
	delete(s.clients, name)
	s.clMu.Unlock()
	writeJSON(w, http.StatusOK, map[string]string{"deleted": name})
}

// proxy forwards a request to the agent's own API.
func (s *Server) proxy(method, path string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		rec, ok := s.store.Agent(r.PathValue("name"))
		if !ok {
			writeErr(w, http.StatusNotFound, errors.New("unknown agent"))
			return
		}
		if method == http.MethodPut && rec.Group != "" {
			writeErr(w, http.StatusConflict, fmt.Errorf("this agent's rules are managed by group %q; edit the group's rules instead", rec.Group))
			return
		}
		p := path
		if q := r.URL.RawQuery; q != "" {
			p += "?" + q
		}
		hdr := http.Header{}
		var body io.Reader
		if method != http.MethodGet {
			body = http.MaxBytesReader(w, r.Body, maxProxyBody)
			hdr.Set("Content-Type", r.Header.Get("Content-Type"))
			hdr.Set("X-Rules-Origin", "dashboard")
		}
		resp, err := s.client(rec).call(r.Context(), method, p, body, hdr)
		if err != nil {
			writeErr(w, http.StatusBadGateway, err)
			return
		}
		defer resp.Body.Close()
		for _, h := range []string{"Content-Type", "X-Rules-Hash"} {
			if v := resp.Header.Get(h); v != "" {
				w.Header().Set(h, v)
			}
		}
		w.WriteHeader(resp.StatusCode)
		io.Copy(w, io.LimitReader(resp.Body, maxProxyBody))
	}
}

// ---- groups ----

type groupRequest struct {
	Name        string            `json:"name"`
	Description string            `json:"description"`
	Source      string            `json:"source"`
	Git         *GitSource        `json:"git"`
	Selector    map[string]string `json:"selector"`
	Fleet       FleetSettings     `json:"fleet"`
	OpsWebhook  string            `json:"ops_webhook_url"`
	// Rules optionally seeds a manual group's rule set on creation.
	Rules string `json:"rules"`
}

func (s *Server) handleAddGroup(w http.ResponseWriter, r *http.Request) {
	var req groupRequest
	if !readJSON(w, r, &req) {
		return
	}
	g := GroupRecord{Name: strings.TrimSpace(req.Name), Description: req.Description, Source: req.Source, Git: req.Git, Selector: req.Selector, Fleet: req.Fleet, OpsWebhookURL: strings.TrimSpace(req.OpsWebhook)}
	if req.Rules != "" && req.Source == "manual" {
		if _, err := validateForDashboard([]byte(req.Rules)); err != nil {
			writeErr(w, http.StatusUnprocessableEntity, err)
			return
		}
		g.Rules = req.Rules
	}
	if err := s.store.AddGroup(g); err != nil {
		writeErr(w, http.StatusBadRequest, err)
		return
	}
	s.log.Info("group created", "group", g.Name, "source", g.Source)
	resp := map[string]any{"name": g.Name}
	// Fetch from git if needed, and push to members (including agents its
	// label selector just picked up).
	if err := s.syncGroup(r.Context(), g.Name, "created", g.Source == "git"); err != nil {
		resp["sync_error"] = err.Error()
	}
	writeJSON(w, http.StatusOK, resp)
}

func (s *Server) handleUpdateGroup(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("name")
	var req groupRequest
	if !readJSON(w, r, &req) {
		return
	}
	g := GroupRecord{Name: name, Description: req.Description, Source: req.Source, Git: req.Git, Selector: req.Selector, Fleet: req.Fleet, OpsWebhookURL: strings.TrimSpace(req.OpsWebhook)}
	if err := s.store.UpdateGroup(g); err != nil {
		writeErr(w, http.StatusBadRequest, err)
		return
	}
	resp := map[string]any{"name": name}
	if err := s.syncGroup(r.Context(), name, "settings changed", g.Source == "git"); err != nil {
		resp["sync_error"] = err.Error()
	}
	writeJSON(w, http.StatusOK, resp)
}

func (s *Server) handleDeleteGroup(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("name")
	if err := s.store.DeleteGroup(name); err != nil {
		writeErr(w, http.StatusNotFound, err)
		return
	}
	s.stMu.Lock()
	delete(s.groupStates, name)
	s.stMu.Unlock()
	writeJSON(w, http.StatusOK, map[string]string{"deleted": name})
}

func (s *Server) handleGroupRules(w http.ResponseWriter, r *http.Request) {
	g, ok := s.store.Group(r.PathValue("name"))
	if !ok {
		writeErr(w, http.StatusNotFound, errors.New("group not found"))
		return
	}
	w.Header().Set("Content-Type", "application/yaml")
	w.Header().Set("X-Rules-Hash", g.RulesHash)
	w.Write([]byte(g.Rules))
}

func (s *Server) handleSetGroupRules(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("name")
	g, ok := s.store.Group(name)
	if !ok {
		writeErr(w, http.StatusNotFound, errors.New("group not found"))
		return
	}
	if g.Source != "manual" {
		writeErr(w, http.StatusConflict, errors.New("this group's rules come from git; change them there and sync"))
		return
	}
	raw, err := io.ReadAll(http.MaxBytesReader(w, r.Body, maxRulesSize))
	if err != nil {
		writeErr(w, http.StatusRequestEntityTooLarge, err)
		return
	}
	c, err := validateForDashboard(raw)
	if err != nil {
		writeErr(w, http.StatusUnprocessableEntity, err)
		return
	}
	if err := s.store.SetGroupRules(name, raw); err != nil {
		writeErr(w, http.StatusInternalServerError, err)
		return
	}
	if err := s.syncGroup(r.Context(), name, "rules edited", false); err != nil && !errors.Is(err, errSyncing) {
		writeErr(w, http.StatusBadGateway, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"hash": c.Hash, "rules": len(c.Rules), "state": s.snapshotState(name)})
}

func (s *Server) handleValidate(w http.ResponseWriter, r *http.Request) {
	raw, err := io.ReadAll(http.MaxBytesReader(w, r.Body, maxRulesSize))
	if err != nil {
		writeErr(w, http.StatusRequestEntityTooLarge, err)
		return
	}
	c, err := validateForDashboard(raw)
	if err != nil {
		writeErr(w, http.StatusUnprocessableEntity, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"hash": c.Hash, "rules": len(c.Rules)})
}

func (s *Server) handleSyncGroup(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("name")
	if _, ok := s.store.Group(name); !ok {
		writeErr(w, http.StatusNotFound, errors.New("group not found"))
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 60*time.Second)
	defer cancel()
	if err := s.syncGroup(ctx, name, "manual", true); err != nil {
		writeErr(w, http.StatusBadGateway, err)
		return
	}
	writeJSON(w, http.StatusOK, s.snapshotState(name))
}

// handlePreview fetches a git group's rules without applying them.
func (s *Server) handlePreview(w http.ResponseWriter, r *http.Request) {
	g, ok := s.store.Group(r.PathValue("name"))
	if !ok || g.Git == nil {
		writeErr(w, http.StatusNotFound, errors.New("not a git group"))
		return
	}
	raw, err := fetchSource(r.Context(), s.git, *g.Git)
	if err != nil {
		writeErr(w, http.StatusBadGateway, err)
		return
	}
	w.Header().Set("Content-Type", "application/yaml")
	w.Header().Set("X-Rules-Hash", rules.Hash(raw))
	w.Write(raw)
}

func writeJSON(w http.ResponseWriter, code int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	json.NewEncoder(w).Encode(v)
}

func writeErr(w http.ResponseWriter, code int, err error) {
	writeJSON(w, code, map[string]string{"error": err.Error()})
}

// Shutdown releases held agent polls so an HTTP server shutdown does not wait
// for them. Agents simply reconnect to the next dashboard instance.
func (s *Server) Shutdown() {
	s.stoppingOnce.Do(func() { close(s.stopping) })
}
