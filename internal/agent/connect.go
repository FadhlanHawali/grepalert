package agent

import (
	"bytes"
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"net/http"
	"net/http/httptest"
	"os"
	"runtime"
	"strings"
	"sync/atomic"
	"time"
)

// DashboardConfig makes the agent connect out to a dashboard, so the
// dashboard can manage it without reaching the agent (no inbound port, works
// behind NAT and HTTP proxies).
type DashboardConfig struct {
	URL string `yaml:"url"`
	// EnrollmentToken is used once, to register; the agent then keeps its own
	// credential in IdentityFile.
	EnrollmentToken    string `yaml:"enrollment_token"`
	CAFile             string `yaml:"ca_file"`
	InsecureSkipVerify bool   `yaml:"insecure_skip_verify"`
}

// identity is the credential the dashboard issued at enrollment.
type identity struct {
	Dashboard string `json:"dashboard"`
	AgentID   string `json:"agent_id"`
	Secret    string `json:"secret"`
	Name      string `json:"name"`
}

// Mirrors of the dashboard's tunnel messages (see dashboard/tunnel.go).
type tunnelRequest struct {
	ID       string      `json:"id"`
	Method   string      `json:"method"`
	Path     string      `json:"path"`
	Header   http.Header `json:"header,omitempty"`
	Body     []byte      `json:"body,omitempty"`
	Deadline time.Time   `json:"deadline"`
}

type tunnelResponse struct {
	ID     string      `json:"id"`
	Status int         `json:"status"`
	Header http.Header `json:"header,omitempty"`
	Body   []byte      `json:"body,omitempty"`
	Error  string      `json:"error,omitempty"`
}

// DashboardStatus is the connection state shown in the agent's status.
type DashboardStatus struct {
	URL         string    `json:"url"`
	AgentID     string    `json:"agent_id,omitempty"`
	Connected   bool      `json:"connected"`
	LastContact time.Time `json:"last_contact,omitempty"`
	LastError   string    `json:"last_error,omitempty"`
}

const (
	maxTunnelBody   = 2 << 20
	tunnelWorkers   = 4
	maxPollWait     = 60 * time.Second // > the dashboard's 25s hold
	maxRetryBackoff = 30 * time.Second
	revokedRetry    = 15 * time.Second // how soon "allow again" takes effect
)

var (
	errUnknownAgent = errors.New("the dashboard does not know this agent")
	errRevoked      = errors.New("the dashboard revoked this agent's access")
)

type connector struct {
	a       *Agent
	cfg     DashboardConfig
	base    string
	client  *http.Client
	handler http.Handler
	id      atomic.Pointer[identity] // written by run, read by respond
	resp    chan *tunnelResponse
	sem     chan struct{}
	state   atomic.Pointer[DashboardStatus]
}

func newConnector(a *Agent) (*connector, error) {
	cfg := a.cfg.Dashboard
	tlsCfg := &tls.Config{InsecureSkipVerify: cfg.InsecureSkipVerify}
	if cfg.CAFile != "" {
		pem, err := os.ReadFile(cfg.CAFile)
		if err != nil {
			return nil, fmt.Errorf("dashboard.ca_file: %w", err)
		}
		pool := x509.NewCertPool()
		if !pool.AppendCertsFromPEM(pem) {
			return nil, errors.New("dashboard.ca_file: no certificates found")
		}
		tlsCfg.RootCAs = pool
	}
	tr := &http.Transport{
		Proxy:               http.ProxyFromEnvironment, // corporate proxies via HTTPS_PROXY
		TLSClientConfig:     tlsCfg,
		MaxIdleConnsPerHost: 2,
		IdleConnTimeout:     90 * time.Second,
		TLSHandshakeTimeout: 10 * time.Second,
	}
	c := &connector{
		a:       a,
		cfg:     cfg,
		base:    strings.TrimSuffix(cfg.URL, "/"),
		client:  &http.Client{Transport: tr, Timeout: maxPollWait},
		handler: a.api(),
		resp:    make(chan *tunnelResponse, 64),
		sem:     make(chan struct{}, tunnelWorkers),
	}
	c.state.Store(&DashboardStatus{URL: c.base})
	return c, nil
}

func (c *connector) status() DashboardStatus { return *c.state.Load() }

func (c *connector) setState(connected bool, err error) {
	st := *c.state.Load()
	st.Connected = connected
	if id := c.id.Load(); id != nil {
		st.AgentID = id.AgentID
	}
	if connected {
		st.LastContact, st.LastError = time.Now(), ""
	} else if err != nil {
		if st.LastError != err.Error() {
			c.a.log.Warn("dashboard connection", "url", c.base, "err", err)
		}
		st.LastError = err.Error()
	}
	c.state.Store(&st)
}

func (c *connector) run(ctx context.Context) {
	go c.respond(ctx)
	c.id.Store(c.loadIdentity())
	backoff := time.Second
	for ctx.Err() == nil {
		if c.id.Load() == nil {
			if err := c.enroll(ctx); err != nil {
				c.setState(false, err)
				sleepCtx(ctx, backoff)
				backoff = min(backoff*2, maxRetryBackoff)
				continue
			}
		}
		reqs, err := c.poll(ctx)
		switch {
		case ctx.Err() != nil:
			return
		case errors.Is(err, errUnknownAgent):
			// e.g. the dashboard's data was reset: enroll again.
			c.setState(false, err)
			c.id.Store(nil)
			os.Remove(c.a.cfg.IdentityFile)
			continue
		case errors.Is(err, errRevoked):
			c.setState(false, err)
			sleepCtx(ctx, revokedRetry)
			continue
		case err != nil:
			c.setState(false, err)
			sleepCtx(ctx, backoff)
			backoff = min(backoff*2, maxRetryBackoff)
			continue
		}
		backoff = time.Second
		c.setState(true, nil)
		for _, r := range reqs {
			select {
			case c.sem <- struct{}{}:
			case <-ctx.Done():
				return
			}
			go func(r *tunnelRequest) {
				defer func() { <-c.sem }()
				c.resp <- c.handle(r)
			}(r)
		}
	}
}

func sleepCtx(ctx context.Context, d time.Duration) {
	select {
	case <-ctx.Done():
	case <-time.After(d):
	}
}

func (c *connector) loadIdentity() *identity {
	raw, err := os.ReadFile(c.a.cfg.IdentityFile)
	if err != nil {
		if !errors.Is(err, fs.ErrNotExist) {
			c.a.log.Warn("cannot read identity file", "path", c.a.cfg.IdentityFile, "err", err)
		}
		return nil
	}
	var id identity
	if err := json.Unmarshal(raw, &id); err != nil || id.AgentID == "" || id.Secret == "" {
		c.a.log.Warn("ignoring invalid identity file", "path", c.a.cfg.IdentityFile)
		return nil
	}
	if id.Dashboard != c.base {
		c.a.log.Info("dashboard URL changed, enrolling again", "old", id.Dashboard, "new", c.base)
		return nil
	}
	return &id
}

func (c *connector) labels() map[string]string {
	l := map[string]string{"os": runtime.GOOS}
	for k, v := range c.a.cfg.Labels {
		l[k] = v
	}
	return l
}

func (c *connector) enroll(ctx context.Context) error {
	if c.cfg.EnrollmentToken == "" {
		return errors.New("not enrolled yet and no dashboard.enrollment_token is configured")
	}
	body, _ := json.Marshal(map[string]any{
		"token": c.cfg.EnrollmentToken, "name": c.a.cfg.Name, "hostname": c.a.host,
		"version": Version, "labels": c.labels(),
	})
	var out struct {
		AgentID string `json:"agent_id"`
		Secret  string `json:"secret"`
		Name    string `json:"name"`
		Error   string `json:"error"`
	}
	code, err := c.post(ctx, "/agent/v1/enroll", "", body, &out)
	if err != nil {
		return fmt.Errorf("enroll: %w", err)
	}
	if code != http.StatusOK {
		return fmt.Errorf("enroll refused (HTTP %d): %s", code, out.Error)
	}
	id := &identity{Dashboard: c.base, AgentID: out.AgentID, Secret: out.Secret, Name: out.Name}
	raw, _ := json.MarshalIndent(id, "", "  ")
	if err := writeFileAtomic(c.a.cfg.IdentityFile, raw); err != nil {
		return fmt.Errorf("save identity: %w", err)
	}
	c.id.Store(id)
	c.a.log.Info("enrolled with dashboard", "url", c.base, "agent_id", id.AgentID, "name", id.Name)
	c.a.events.add(Event{Time: time.Now(), Type: "rules", Message: "enrolled with dashboard " + c.base})
	return nil
}

func (c *connector) poll(ctx context.Context) ([]*tunnelRequest, error) {
	body, _ := json.Marshal(map[string]any{"hostname": c.a.host, "version": Version, "labels": c.labels()})
	id := c.id.Load()
	resp, err := c.send(ctx, "/agent/v1/poll", id.AgentID+"."+id.Secret, body)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	var out struct {
		Requests []*tunnelRequest `json:"requests"`
		Error    string           `json:"error"`
		Code     string           `json:"code"`
	}
	if resp.StatusCode == http.StatusOK {
		// The dashboard sends headers right away and holds the body until it
		// has work, so we are connected from this point on.
		c.setState(true, nil)
	}
	if err := json.NewDecoder(io.LimitReader(resp.Body, 8*maxTunnelBody)).Decode(&out); err != nil && resp.StatusCode == http.StatusOK {
		return nil, fmt.Errorf("poll: %w", err)
	}
	switch {
	case resp.StatusCode == http.StatusOK:
		return out.Requests, nil
	case out.Code == "unknown_agent":
		return nil, errUnknownAgent
	case out.Code == "revoked":
		return nil, errRevoked
	default:
		return nil, fmt.Errorf("poll: HTTP %d: %s", resp.StatusCode, out.Error)
	}
}

// respond sends finished responses back, batching whatever is ready.
func (c *connector) respond(ctx context.Context) {
	for {
		var batch []*tunnelResponse
		select {
		case <-ctx.Done():
			return
		case r := <-c.resp:
			batch = append(batch, r)
		}
	drain:
		for len(batch) < 16 {
			select {
			case r := <-c.resp:
				batch = append(batch, r)
			default:
				break drain
			}
		}
		id := c.id.Load()
		if id == nil {
			continue
		}
		body, _ := json.Marshal(map[string]any{"responses": batch})
		for attempt := 0; attempt < 3; attempt++ {
			code, err := c.post(ctx, "/agent/v1/respond", id.AgentID+"."+id.Secret, body, nil)
			if err == nil && code < 300 {
				break
			}
			sleepCtx(ctx, time.Duration(attempt+1)*500*time.Millisecond)
		}
	}
}

// handle runs one tunneled request against the agent's own API.
func (c *connector) handle(r *tunnelRequest) *tunnelResponse {
	out := &tunnelResponse{ID: r.ID}
	if !r.Deadline.IsZero() && time.Now().After(r.Deadline) {
		out.Error = "request expired before the agent received it"
		return out
	}
	ctx, cancel := context.WithDeadline(context.Background(), r.Deadline)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, r.Method, "http://agent.local"+r.Path, bytes.NewReader(r.Body))
	if err != nil || !strings.HasPrefix(r.Path, "/api/v1/") {
		out.Error = "bad tunnel request"
		return out
	}
	for k, v := range r.Header {
		req.Header[k] = v
	}
	rec := httptest.NewRecorder()
	c.handler.ServeHTTP(rec, req)
	out.Status, out.Header = rec.Code, rec.Header()
	out.Body = rec.Body.Bytes()
	if len(out.Body) > maxTunnelBody {
		out.Body = out.Body[:maxTunnelBody]
	}
	return out
}

// send POSTs a JSON body to the dashboard; the caller closes the response.
func (c *connector) send(ctx context.Context, path, cred string, body []byte) (*http.Response, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.base+path, bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("User-Agent", "grepalert-agent/"+Version)
	if cred != "" {
		req.Header.Set("Authorization", "Bearer "+cred)
	}
	return c.client.Do(req)
}

// post is send plus decoding a JSON reply into out.
func (c *connector) post(ctx context.Context, path, cred string, body []byte, out any) (int, error) {
	resp, err := c.send(ctx, path, cred, body)
	if err != nil {
		return 0, err
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(io.LimitReader(resp.Body, 8*maxTunnelBody))
	if out != nil && len(raw) > 0 {
		if err := json.Unmarshal(raw, out); err != nil && resp.StatusCode == http.StatusOK {
			return resp.StatusCode, fmt.Errorf("unexpected response from %s: %w", path, err)
		}
	}
	return resp.StatusCode, nil
}
