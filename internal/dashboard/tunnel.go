package dashboard

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"sync"
	"sync/atomic"
	"time"
)

// Connected agents keep an outbound long-poll to the dashboard. The dashboard
// sends them the same HTTP API calls it makes to direct agents, wrapped as
// TunnelRequests; the agent runs each one against its own API in-process and
// posts back a TunnelResponse. This keeps every agent behind NAT/firewalls
// reachable without opening an inbound port, and works through HTTP proxies.
//
// Wire protocol (all JSON, agent → dashboard, Authorization: Bearer id.secret):
//
//	POST /agent/v1/enroll   EnrollRequest            → {agent_id, secret, name}
//	POST /agent/v1/poll     PollRequest (heartbeat)  → {requests: [TunnelRequest]}  (held up to pollHold)
//	POST /agent/v1/respond  {responses: [TunnelResponse]}

const (
	pollHold = 25 * time.Second
	// An agent counts as online while it is polling, or briefly after a poll
	// ended: agents start their next poll immediately, so a longer silence
	// means it is gone (or revoked) and calls should fail fast.
	onlineGrace = 5 * time.Second
	maxPerPoll  = 16
)

type TunnelRequest struct {
	ID       string      `json:"id"`
	Method   string      `json:"method"`
	Path     string      `json:"path"`
	Header   http.Header `json:"header,omitempty"`
	Body     []byte      `json:"body,omitempty"`
	Deadline time.Time   `json:"deadline"`
}

type TunnelResponse struct {
	ID     string      `json:"id"`
	Status int         `json:"status"`
	Header http.Header `json:"header,omitempty"`
	Body   []byte      `json:"body,omitempty"`
	Error  string      `json:"error,omitempty"`
}

type PollRequest struct {
	Hostname string            `json:"hostname"`
	Version  string            `json:"version"`
	Labels   map[string]string `json:"labels"`
}

// tunnel is the dashboard's side of one connected agent.
type tunnel struct {
	queue    chan *TunnelRequest
	wake     chan struct{}
	mu       sync.Mutex
	pending  map[string]chan *TunnelResponse
	polling  atomic.Int32
	lastSeen atomic.Int64 // unix nanos
}

func (s *Server) tunnelFor(id string) *tunnel {
	s.tunMu.Lock()
	defer s.tunMu.Unlock()
	t := s.tunnels[id]
	if t == nil {
		t = &tunnel{queue: make(chan *TunnelRequest, 64), wake: make(chan struct{}, 1), pending: map[string]chan *TunnelResponse{}}
		s.tunnels[id] = t
	}
	return t
}

func (t *tunnel) online() bool {
	return t.polling.Load() > 0 || time.Since(time.Unix(0, t.lastSeen.Load())) < onlineGrace
}

func (t *tunnel) seen() { t.lastSeen.Store(time.Now().UnixNano()) }

// lastSeenAt returns when the agent last contacted the dashboard.
func (s *Server) lastSeenAt(id string) time.Time {
	s.tunMu.Lock()
	t := s.tunnels[id]
	s.tunMu.Unlock()
	if t == nil || t.lastSeen.Load() == 0 {
		return time.Time{}
	}
	if t.polling.Load() > 0 {
		return time.Now()
	}
	return time.Unix(0, t.lastSeen.Load())
}

// tunnelTransport is an http.RoundTripper that carries requests to a
// connected agent over its poll connection.
type tunnelTransport struct {
	s  *Server
	id string
}

func (tt *tunnelTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	if rec, ok := tt.s.store.AgentByID(tt.id); !ok || rec.Revoked {
		// A revoked agent never collects requests; fail now instead of timing out.
		return nil, errRevoked
	}
	t := tt.s.tunnelFor(tt.id)
	if !t.online() {
		if seen := tt.s.lastSeenAt(tt.id); !seen.IsZero() {
			return nil, fmt.Errorf("agent is offline (last seen %s ago)", time.Since(seen).Round(time.Second))
		}
		return nil, errors.New("agent has not connected to the dashboard yet")
	}
	var body []byte
	if req.Body != nil {
		b, err := io.ReadAll(io.LimitReader(req.Body, maxProxyBody+1))
		req.Body.Close()
		if err != nil {
			return nil, err
		}
		body = b
	}
	deadline, ok := req.Context().Deadline()
	if !ok {
		deadline = time.Now().Add(30 * time.Second)
	}
	tr := &TunnelRequest{ID: randomID(), Method: req.Method, Path: req.URL.RequestURI(), Header: req.Header.Clone(), Body: body, Deadline: deadline}
	tr.Header.Del("Authorization") // the tunnel itself is authenticated

	ch := make(chan *TunnelResponse, 1)
	t.mu.Lock()
	t.pending[tr.ID] = ch
	t.mu.Unlock()
	defer func() {
		t.mu.Lock()
		delete(t.pending, tr.ID)
		t.mu.Unlock()
	}()

	ctx := req.Context()
	select {
	case t.queue <- tr:
	case <-ctx.Done():
		return nil, ctx.Err()
	}
	select {
	case resp := <-ch:
		if resp.Error != "" {
			return nil, errors.New(resp.Error)
		}
		return &http.Response{
			StatusCode:    resp.Status,
			Status:        fmt.Sprintf("%d %s", resp.Status, http.StatusText(resp.Status)),
			Header:        resp.Header,
			Body:          io.NopCloser(bytes.NewReader(resp.Body)),
			ContentLength: int64(len(resp.Body)),
			Request:       req,
			Proto:         "HTTP/1.1", ProtoMajor: 1, ProtoMinor: 1,
		}, nil
	case <-ctx.Done():
		return nil, fmt.Errorf("agent did not answer in time: %w", ctx.Err())
	}
}

// agentAuth authenticates a connected agent's request.
func (s *Server) agentAuth(w http.ResponseWriter, r *http.Request) (AgentRecord, bool) {
	cred, ok := bytesCutPrefix(r.Header.Get("Authorization"), "Bearer ")
	if !ok {
		writeErr(w, http.StatusUnauthorized, errUnknownAgent)
		return AgentRecord{}, false
	}
	a, err := s.store.Authenticate(cred)
	switch {
	case errors.Is(err, errRevoked):
		writeJSON(w, http.StatusForbidden, map[string]string{"error": err.Error(), "code": "revoked"})
		return AgentRecord{}, false
	case err != nil:
		// The agent re-enrolls when it sees this (e.g. dashboard data was reset).
		writeJSON(w, http.StatusUnauthorized, map[string]string{"error": err.Error(), "code": "unknown_agent"})
		return AgentRecord{}, false
	}
	return a, true
}

func bytesCutPrefix(s, prefix string) (string, bool) {
	if len(s) > len(prefix) && s[:len(prefix)] == prefix {
		return s[len(prefix):], true
	}
	return "", false
}

func (s *Server) handleEnroll(w http.ResponseWriter, r *http.Request) {
	var req EnrollRequest
	if !readJSON(w, r, &req) {
		return
	}
	rec, secret, err := s.store.Enroll(req, s.cfg.EnrollmentToken)
	if err != nil {
		code := http.StatusBadRequest
		if errors.Is(err, errBadEnrollment) {
			code = http.StatusUnauthorized
		}
		writeErr(w, code, err)
		return
	}
	s.log.Info("agent enrolled", "agent", rec.Name, "hostname", rec.Hostname, "labels", rec.Labels, "via", rec.EnrolledVia)
	s.audit.system("token:"+rec.EnrolledVia, "agent", "agent.enroll", rec.Name, EffectiveGroup(rec, s.store.Groups()),
		fmt.Sprintf("hostname %s, labels %s, from %s", rec.Hostname, labelsString(rec.Labels), clientIP(r)), "")
	writeJSON(w, http.StatusOK, map[string]string{"agent_id": rec.ID, "secret": secret, "name": rec.Name})
	s.kickMembership(rec.Name)
}

func (s *Server) handlePoll(w http.ResponseWriter, r *http.Request) {
	a, ok := s.agentAuth(w, r)
	if !ok {
		return
	}
	var hb PollRequest
	if !readJSON(w, r, &hb) {
		return
	}
	if changed, err := s.store.UpdateAgentInfo(a.ID, hb.Hostname, hb.Version, hb.Labels); err != nil {
		writeErr(w, http.StatusBadRequest, err)
		return
	} else if changed {
		s.kickMembership(a.Name) // labels may have moved it to another group
	}

	t := s.tunnelFor(a.ID)
	firstContact := t.lastSeen.Load() == 0
	t.seen()
	t.polling.Add(1)
	defer func() { t.polling.Add(-1); t.seen() }()
	if firstContact {
		s.kickMembership(a.Name) // e.g. dashboard restarted: re-check its rules
	}

	// Send the headers now: the agent knows it is connected as soon as they
	// arrive, while the body (the work) follows when there is some.
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	if f, ok := w.(http.Flusher); ok {
		f.Flush()
	}

	var reqs []*TunnelRequest
	timer := time.NewTimer(pollHold)
	defer timer.Stop()
	select {
	case tr := <-t.queue:
		reqs = append(reqs, tr)
	case <-t.wake: // e.g. revoked: end the poll so the agent finds out now
	case <-s.stopping:
	case <-timer.C:
	case <-r.Context().Done():
		return
	}
	for len(reqs) < maxPerPoll {
		select {
		case tr := <-t.queue:
			reqs = append(reqs, tr)
			continue
		default:
		}
		break
	}
	json.NewEncoder(w).Encode(map[string]any{"requests": orEmpty(reqs), "silences": s.silencesFor(a)})
}

// wakeTunnel ends an agent's held poll early.
func (s *Server) wakeTunnel(id string) {
	t := s.tunnelFor(id)
	select {
	case t.wake <- struct{}{}:
	default:
	}
}

func orEmpty[T any](s []T) []T {
	if s == nil {
		return []T{}
	}
	return s
}

func (s *Server) handleRespond(w http.ResponseWriter, r *http.Request) {
	a, ok := s.agentAuth(w, r)
	if !ok {
		return
	}
	var body struct {
		Responses []*TunnelResponse `json:"responses"`
	}
	if err := json.NewDecoder(io.LimitReader(r.Body, 8*maxProxyBody)).Decode(&body); err != nil {
		writeErr(w, http.StatusBadRequest, err)
		return
	}
	t := s.tunnelFor(a.ID)
	t.seen()
	t.mu.Lock()
	for _, resp := range body.Responses {
		if ch, ok := t.pending[resp.ID]; ok {
			ch <- resp // buffered; the waiter may already have given up
			delete(t.pending, resp.ID)
		}
	}
	t.mu.Unlock()
	w.WriteHeader(http.StatusNoContent)
}

// kickMembership pushes group rules to an agent soon, without blocking the
// caller (used when it enrolls, reconnects or its labels change).
func (s *Server) kickMembership(name string) {
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		// Give a freshly enrolled agent a moment to start polling.
		time.Sleep(200 * time.Millisecond)
		a, ok := s.store.Agent(name)
		if !ok {
			return
		}
		if res := s.pushToNewMember(ctx, a); res != nil && !res.OK {
			s.log.Warn("pushing group rules failed", "agent", name, "err", res.Error)
		}
	}()
}
