package agent

import (
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestOwnRulesSavedOnFirstGroupPush(t *testing.T) {
	own := "rules:\n  - {name: mine, files: [/x], pattern: A, webhooks: [h]}\nwebhooks: [{name: h, url: 'http://127.0.0.1:1/'}]\n"
	g1 := "rules:\n  - {name: g1, files: [/x], pattern: B, webhooks: [h]}\nwebhooks: [{name: h, url: 'http://127.0.0.1:1/'}]\n"
	g2 := "rules:\n  - {name: g2, files: [/x], pattern: C, webhooks: [h]}\nwebhooks: [{name: h, url: 'http://127.0.0.1:1/'}]\n"
	a, _ := testAgent(t, "")
	if _, _, err := a.ApplyRules([]byte(own), "api"); err != nil {
		t.Fatal(err)
	}
	if _, ok := a.OwnRules(); ok {
		t.Fatal("no group yet, nothing should be saved")
	}
	// A rejected group push saves nothing.
	if _, _, err := a.ApplyRules([]byte("rules: [{name: x}]"), "group:web"); err == nil {
		t.Fatal("invalid rules accepted")
	}
	if _, ok := a.OwnRules(); ok {
		t.Fatal("a rejected push saved the own rules")
	}
	a.ApplyRules([]byte(g1), "group:web")
	a.ApplyRules([]byte(g2), "group:web") // later versions keep the first copy
	if raw, ok := a.OwnRules(); !ok || string(raw) != own || a.Status().OwnRulesHash == "" {
		t.Fatalf("saved = %q %v", raw, ok)
	}
	if _, err := a.RestoreOwnRules(); err != nil {
		t.Fatal(err)
	}
	if string(a.Rules().Raw) != own {
		t.Fatalf("active after restore = %q", a.Rules().Raw)
	}
	if _, ok := a.OwnRules(); ok || a.Status().OwnRulesHash != "" {
		t.Fatal("the copy should be gone after restoring")
	}
	if _, err := a.RestoreOwnRules(); err == nil {
		t.Fatal("restore without a copy should fail")
	}

	// A direct edit after leaving a group makes those the agent's own rules.
	a.ApplyRules([]byte(g1), "group:web")
	a.ApplyRules([]byte(g2), "dashboard")
	if _, ok := a.OwnRules(); ok {
		t.Fatal("a direct edit should drop the old copy")
	}
}

func TestSilencesSurviveRestart(t *testing.T) {
	dir := t.TempDir()
	cfg := Config{RulesFile: filepath.Join(dir, "rules.yaml"), StateFile: "off",
		Dashboard: DashboardConfig{URL: "http://127.0.0.1:1", EnrollmentToken: "x"}}
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	a := New(cfg, log)
	a.conn.setSilences([]Silence{
		{ID: "s1", Rules: []string{"errors"}, EndsAt: time.Now().Add(time.Hour)},
		{ID: "old", EndsAt: time.Now().Add(-time.Minute)},
	})
	if _, err := os.Stat(filepath.Join(dir, "grepalert.silences.json")); err != nil {
		t.Fatal(err)
	}
	// A new agent process, with the dashboard still unreachable.
	b := New(cfg, log)
	if !b.conn.silenced("errors") || b.conn.silenced("other") {
		t.Fatal("restored silences do not apply")
	}
	if n := len(*b.conn.silences.Load()); n != 1 {
		t.Fatalf("restored %d silences, want 1 (the expired one dropped)", n)
	}
}
