package dashboard

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	"github.com/fadhlanhawali/grepalert/internal/agent"
)

const groupRules = `webhooks:
  - name: w
    url: ${WEBHOOK_URL}
rules:
  - name: errors
    files: [/tmp/none.log]
    pattern: ERROR
    webhooks: [w]
`

type harness struct {
	t     *testing.T
	dash  *httptest.Server
	srv   *Server
	agent *agent.Agent
	aURL  string
}

func newHarness(t *testing.T) *harness {
	t.Helper()
	t.Setenv("WEBHOOK_URL", "http://127.0.0.1:1/hook")
	dir := t.TempDir()
	log := slog.New(slog.NewTextHandler(io.Discard, nil))

	a := agent.New(agent.Config{Name: "agent-a", Token: "tok", RulesFile: filepath.Join(dir, "rules.yaml"), StateFile: "off"}, log)
	as := httptest.NewServer(a.Handler())
	t.Cleanup(as.Close)

	store, err := OpenStore(filepath.Join(dir, "dash.json"))
	if err != nil {
		t.Fatal(err)
	}
	s := New(Config{}, store, log)
	ds := httptest.NewServer(s.Handler())
	t.Cleanup(ds.Close)
	return &harness{t: t, dash: ds, srv: s, agent: a, aURL: as.URL}
}

func (h *harness) do(method, path string, body any, wantCode int) map[string]any {
	h.t.Helper()
	var rd io.Reader
	switch b := body.(type) {
	case nil:
	case string:
		rd = strings.NewReader(b)
	default:
		raw, _ := json.Marshal(b)
		rd = strings.NewReader(string(raw))
	}
	req, _ := http.NewRequest(method, h.dash.URL+path, rd)
	req.Header.Set("X-Requested-With", "grepalert")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		h.t.Fatal(err)
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != wantCode {
		h.t.Fatalf("%s %s: HTTP %d, want %d: %s", method, path, resp.StatusCode, wantCode, raw)
	}
	out := map[string]any{}
	json.Unmarshal(raw, &out)
	return out
}

func TestConnectAgentAndGroupRules(t *testing.T) {
	h := newHarness(t)

	// Wrong token: rejected unless forced.
	r := h.do("POST", "/api/agents", map[string]any{"url": h.aURL, "token": "bad"}, http.StatusUnprocessableEntity)
	if r["unreachable"] != true || !strings.Contains(r["error"].(string), "token") {
		t.Fatalf("unexpected error response: %v", r)
	}

	// Group with rules first, then connect the agent into it: rules are pushed.
	h.do("POST", "/api/groups", map[string]any{"name": "web", "source": "manual", "rules": groupRules}, http.StatusOK)
	r = h.do("POST", "/api/agents", map[string]any{"url": h.aURL, "token": "tok", "group": "web"}, http.StatusOK)
	if r["name"] != "agent-a" {
		t.Fatalf("name should default to the agent's own name, got %v", r["name"])
	}
	if push := r["push"].(map[string]any); push["ok"] != true || push["action"] != "applied" {
		t.Fatalf("push = %v", push)
	}
	g, _ := h.srv.store.Group("web")
	if h.agent.Rules().Hash != g.RulesHash {
		t.Fatal("agent does not run the group's rules")
	}

	// Editing a managed agent's rules directly is refused.
	h.do("PUT", "/api/agents/agent-a/rules", "rules: []\n", http.StatusConflict)

	// Drift (someone changed the agent locally) is repaired by a sync.
	if _, _, err := h.agent.ApplyRules([]byte("rules: []\n"), "local"); err != nil {
		t.Fatal(err)
	}
	if err := h.srv.syncGroup(context.Background(), "web", "schedule", false); err != nil {
		t.Fatal(err)
	}
	if h.agent.Rules().Hash != g.RulesHash {
		t.Fatal("drift was not repaired")
	}
	if res := h.srv.snapshotState("web").Results["agent-a"]; res.Action != "applied" {
		t.Fatalf("result = %+v", res)
	}

	// Editing the group's rules pushes the new version.
	newRules := strings.Replace(groupRules, "ERROR", "FATAL", 1)
	h.do("PUT", "/api/groups/web/rules", newRules, http.StatusOK)
	if got := string(h.agent.Rules().Raw); got != newRules {
		t.Fatalf("agent rules not updated:\n%s", got)
	}

	// Invalid group rules are rejected and nothing changes.
	h.do("PUT", "/api/groups/web/rules", "rules:\n  - name: x\n    pattern: '('\n", http.StatusUnprocessableEntity)

	// Deleting the group leaves the agent ungrouped with its rules.
	h.do("DELETE", "/api/groups/web", nil, http.StatusOK)
	a, _ := h.srv.store.Agent("agent-a")
	if a.Group != "" || string(h.agent.Rules().Raw) != newRules {
		t.Fatal("deleting the group should ungroup the agent and keep its rules")
	}
	h.do("PUT", "/api/agents/agent-a/rules", groupRules, http.StatusOK) // editable again

	// Overview never exposes tokens.
	ov := h.do("GET", "/api/overview", nil, http.StatusOK)
	if raw, _ := json.Marshal(ov); strings.Contains(string(raw), `"tok"`) {
		t.Fatal("overview leaked the agent token")
	}
}

func TestGitGroup(t *testing.T) {
	h := newHarness(t)
	content := groupRules
	repo := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { io.WriteString(w, content) }))
	defer repo.Close()

	h.do("POST", "/api/agents", map[string]any{"url": h.aURL, "token": "tok"}, http.StatusOK)
	h.do("POST", "/api/groups", map[string]any{"name": "g", "source": "git",
		"git": map[string]any{"provider": "url", "path": repo.URL + "/rules.yaml"}}, http.StatusOK)
	h.do("PUT", "/api/agents/agent-a", map[string]any{"url": h.aURL, "group": "g"}, http.StatusOK)
	if string(h.agent.Rules().Raw) != content {
		t.Fatal("git rules not pushed to the new member")
	}

	// Rules come from git: editing them in the dashboard is refused.
	h.do("PUT", "/api/groups/g/rules", groupRules, http.StatusConflict)

	// New commit + sync: applied. Broken commit: rejected, agent keeps the last good rules.
	content = strings.Replace(groupRules, "ERROR", "PANIC", 1)
	h.do("POST", "/api/groups/g/sync", nil, http.StatusOK)
	if string(h.agent.Rules().Raw) != content {
		t.Fatal("new git version not applied")
	}
	good := content
	content = "rules: [oops"
	h.do("POST", "/api/groups/g/sync", nil, http.StatusBadGateway)
	if string(h.agent.Rules().Raw) != good {
		t.Fatal("broken git content must not reach agents")
	}
}
