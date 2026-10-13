// Package agent implements the grepalert agent: it tails log files, matches
// lines against hot-reloadable rules, suppresses floods and posts alerts to
// webhooks.
package agent

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io/fs"
	"log/slog"
	"os"
	"path/filepath"
	"runtime"
	"runtime/debug"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/fadhlanhawali/grepalert/internal/rules"
)

var Version = "0.1.0"

type Agent struct {
	cfg     Config
	log     *slog.Logger
	host    string
	started time.Time

	events  *ring
	eng     *engine
	notif   *notifier
	conn    *connector // nil unless dashboard.url is set
	connErr error

	mu            sync.Mutex // guards everything below
	rules         *rules.Compiled
	rulesLoadedAt time.Time
	rulesOrigin   string
	tailers       map[string]*tailerHandle
	resume        map[string]Position // saved positions not yet handed to a tailer
	stopped       bool
	sched         *scheduler // polls all tailers on a fixed worker pool

	lastState []byte // last state written, to skip identical writes
}

type tailerHandle struct {
	t *tailer
}

func New(cfg Config, log *slog.Logger) *Agent {
	cfg.setDefaults()
	host, _ := os.Hostname()
	if cfg.Name == "" {
		cfg.Name = host
	}
	a := &Agent{
		cfg:     cfg,
		log:     log,
		host:    host,
		started: time.Now(),
		events:  newRing(cfg.EventBuffer),
		tailers: map[string]*tailerHandle{},
		rules:   &rules.Compiled{Hash: rules.Hash(nil), Webhooks: map[string]*rules.CompiledWebhook{}},
	}
	a.notif = newNotifier(cfg.QueueSize, cfg.Retries, a.events)
	a.eng = newEngine(cfg.Name, host, a.events, a.notif.enqueue)
	a.sched = newScheduler(cfg.ReadWorkers, cfg.PollInterval.D(), cfg.IdlePollInterval.D())
	if cfg.Dashboard.URL != "" {
		a.conn, a.connErr = newConnector(a)
		if a.conn != nil {
			// Alerts go through the dashboard, directly only as a fallback.
			a.notif.route = a.conn.offer
			a.notif.silenced = a.conn.silenced
			a.conn.fallback = a.notif.fallbackDeliver
		}
	}
	return a
}

// Run loads the rules file, starts tailing and blocks until ctx is cancelled.
func (a *Agent) Run(ctx context.Context) error {
	if a.cfg.MemoryLimit > 0 {
		debug.SetMemoryLimit(int64(a.cfg.MemoryLimit))
	}
	// The notifier outlives ctx: on shutdown it is stopped only after the
	// tailers, so alerts for the last lines read are still delivered.
	notifCtx, stopNotif := context.WithCancel(context.Background())
	defer stopNotif()
	a.notif.start(notifCtx, a.cfg.Workers)

	if a.stateEnabled() {
		pos, err := loadState(a.cfg.StateFile)
		if err != nil {
			a.log.Warn("cannot read state file, starting without saved positions", "path", a.cfg.StateFile, "err", err)
		}
		a.mu.Lock()
		a.resume = pos
		a.mu.Unlock()
		if len(pos) > 0 {
			a.log.Info("loaded saved file positions", "files", len(pos), "path", a.cfg.StateFile)
		}
	}

	if a.connErr != nil {
		return a.connErr
	}
	if err := a.loadRulesFile(true); err != nil {
		return err
	}
	if a.conn != nil {
		go a.conn.run(ctx)
	}

	tick := time.NewTicker(a.cfg.RescanInterval.D())
	defer tick.Stop()
	sweep := time.NewTicker(time.Second)
	defer sweep.Stop()
	saveEvery := a.cfg.StateInterval.D()
	if !a.stateEnabled() {
		saveEvery = time.Hour // ticker needs a positive interval; saves are no-ops
	}
	save := time.NewTicker(saveEvery)
	defer save.Stop()
	for {
		select {
		case <-ctx.Done():
			a.mu.Lock()
			a.stopped = true
			handles := make([]*tailerHandle, 0, len(a.tailers))
			for _, h := range a.tailers {
				handles = append(handles, h)
			}
			a.mu.Unlock()
			a.sched.stop() // final read of every file, then close
			if a.conn != nil {
				a.conn.flushAlerts() // hand the last alerts to the dashboard (or send them)
			}
			stopNotif()
			a.notif.wait()
			// Save after the tailers stopped, so the final positions are exact.
			a.saveState(handles)
			return nil
		case <-save.C:
			a.saveState(nil)
		case <-sweep.C:
			a.eng.sweep()
		case <-tick.C:
			if err := a.loadRulesFile(false); err != nil {
				a.log.Error("rules file reload failed, keeping previous rules", "err", err)
			}
			a.mu.Lock()
			a.reconcile(true)
			a.mu.Unlock()
		}
	}
}

// loadRulesFile (re)loads the rules file if its content changed, so edits made
// directly on disk are picked up without a restart.
func (a *Agent) loadRulesFile(initial bool) error {
	raw, err := os.ReadFile(a.cfg.RulesFile)
	if errors.Is(err, fs.ErrNotExist) && initial {
		a.log.Warn("rules file does not exist yet, starting with no rules", "path", a.cfg.RulesFile)
		raw, err = nil, nil
	}
	if err != nil {
		return err
	}
	a.mu.Lock()
	same := a.rules != nil && a.rules.Hash == rules.Hash(raw)
	a.mu.Unlock()
	if same {
		return nil
	}
	_, err = a.apply(raw, "file", false)
	if err != nil && !initial {
		a.events.add(Event{Time: time.Now(), Type: "rules", Message: "rules file rejected: " + err.Error()})
	}
	return err
}

// ApplyRules validates raw, persists it to the rules file and activates it.
// On validation failure nothing changes. changed is false when raw is identical
// to the active rules, in which case nothing is done.
func (a *Agent) ApplyRules(raw []byte, origin string) (c *rules.Compiled, changed bool, err error) {
	if cur := a.Rules(); cur.Hash == rules.Hash(raw) {
		return cur, false, nil
	}
	c, err = a.apply(raw, origin, true)
	return c, err == nil, err
}

func (a *Agent) apply(raw []byte, origin string, persist bool) (*rules.Compiled, error) {
	c, err := rules.Parse(raw)
	if err != nil {
		return nil, err
	}
	if persist {
		if err := writeFileAtomic(a.cfg.RulesFile, raw); err != nil {
			return nil, fmt.Errorf("persist rules: %w", err)
		}
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	a.rules = c
	a.rulesLoadedAt = time.Now()
	a.rulesOrigin = origin
	a.reconcile(false)
	a.log.Info("rules applied", "hash", c.Hash, "rules", len(c.Rules), "origin", origin)
	a.events.add(Event{Time: time.Now(), Type: "rules",
		Message: fmt.Sprintf("rules %s applied from %s (%d active rules)", c.Hash, origin, len(c.Rules))})
	return c, nil
}

// reconcile expands file globs, starts/stops tailers to match and publishes a
// new snapshot to the engine. Callers must hold a.mu.
//
// discovered is true on periodic re-scans: files that appear then are new and
// are read from the beginning; files picked up because rules changed start at
// the end so old history does not fire a burst of alerts.
func (a *Agent) reconcile(discovered bool) {
	if a.stopped {
		return
	}
	byFile := map[string][]*rules.CompiledRule{}
	for _, r := range a.rules.Rules {
		for _, pat := range r.Files {
			for _, p := range expandPath(pat) {
				byFile[p] = append(byFile[p], r)
			}
		}
	}
	for p, h := range a.tailers {
		if _, ok := byFile[p]; !ok {
			a.sched.remove(h.t)
			delete(a.tailers, p)
		}
	}
	fromStart := a.cfg.StartFrom == "beginning"
	for p := range byFile {
		if _, ok := a.tailers[p]; ok {
			continue
		}
		// A saved position wins over start_from; it is used once, so a file
		// dropped from the rules and re-added later starts fresh.
		var resume *Position
		if pos, ok := a.resume[p]; ok {
			resume = &pos
			delete(a.resume, p)
		}
		t := newTailer(p, a.cfg.PollInterval.D(), a.cfg.MaxLineBytes, fromStart || discovered, resume,
			a.eng.process, a.tailError, a.log.With("file", p))
		a.tailers[p] = &tailerHandle{t: t}
		a.sched.add(t)
	}
	a.eng.setSnapshot(&snapshot{rules: a.rules, byFile: byFile})
}

// expandPath returns glob matches, or the literal path when it has no glob
// characters (so we wait for files that do not exist yet).
func expandPath(pat string) []string {
	if !strings.ContainsAny(pat, "*?[") {
		return []string{filepath.Clean(pat)}
	}
	m, _ := filepath.Glob(pat)
	return m
}

// tailError is called by tailers, which already de-duplicate repeated errors.
func (a *Agent) tailError(path string, err error) {
	a.log.Warn("tail error", "file", path, "err", err)
	a.events.add(Event{Time: time.Now(), Type: "error", File: path, Message: err.Error()})
}

func (a *Agent) Rules() *rules.Compiled {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.rules
}

func (a *Agent) Events(limit int) []Event { return a.events.list(limit) }

// TestLine reports which active rules would match line (ignoring files).
func (a *Agent) TestLine(line string) []string {
	var hits []string
	for _, r := range a.Rules().Rules {
		if r.Re.MatchString(line) && (r.ExcludeRe == nil || !r.ExcludeRe.MatchString(line)) {
			hits = append(hits, r.Name)
		}
	}
	return hits
}

type Status struct {
	Name          string       `json:"name"`
	Host          string       `json:"host"`
	Version       string       `json:"version"`
	StartedAt     time.Time    `json:"started_at"`
	RulesHash     string       `json:"rules_hash"`
	RulesLoadedAt time.Time    `json:"rules_loaded_at"`
	RulesOrigin   string       `json:"rules_origin"`
	Rules         []RuleStatus `json:"rules"`
	Files         []FileStatus `json:"files"`
	Webhooks      struct {
		Sent    uint64 `json:"sent"`
		Failed  uint64 `json:"failed"`
		Dropped uint64 `json:"dropped"`
		Queued  int    `json:"queued"`
		// Connected agents: alerts handed to the dashboard, sent directly
		// because it was unreachable, and dropped because it had silenced them.
		ViaDashboard uint64 `json:"via_dashboard"`
		Fallback     uint64 `json:"fallback"`
		Silenced     uint64 `json:"silenced"`
	} `json:"webhooks"`
	SuppressKeys int              `json:"suppress_keys"`
	Dashboard    *DashboardStatus `json:"dashboard,omitempty"`
	Memory       MemoryStats      `json:"memory"`
}

type RuleStatus struct {
	Name     string   `json:"name"`
	Pattern  string   `json:"pattern"`
	Files    []string `json:"files"`
	Suppress string   `json:"suppress,omitempty"`
	// Absence rules: the expected line, how long it may be missing, whether
	// the rule is firing and when the line was last seen.
	Absent       string    `json:"absent,omitempty"`
	For          string    `json:"for,omitempty"`
	Firing       bool      `json:"firing,omitempty"`
	LastSeenLine time.Time `json:"last_seen_line,omitempty"`
	RuleStats
}

type FileStatus struct {
	Path   string   `json:"path"`
	Open   bool     `json:"open"`
	Offset int64    `json:"offset"`
	Lines  uint64   `json:"lines"`
	Rules  []string `json:"rules"`
	Error  string   `json:"error,omitempty"`
}

type MemoryStats struct {
	HeapAlloc   uint64 `json:"heap_alloc"`
	HeapInuse   uint64 `json:"heap_inuse"`
	Sys         uint64 `json:"sys"`
	NumGC       uint32 `json:"num_gc"`
	Goroutines  int    `json:"goroutines"`
	MemoryLimit int64  `json:"memory_limit"`
	// Breakdown of what counts against the memory limit.
	StackInuse   uint64  `json:"stack_inuse"`
	HeapIdle     uint64  `json:"heap_idle"`
	HeapReleased uint64  `json:"heap_released"`
	GCCPU        float64 `json:"gc_cpu_fraction"`
}

func (a *Agent) Status() Status {
	var s Status
	s.Name, s.Host, s.Version, s.StartedAt = a.cfg.Name, a.host, Version, a.started

	stats := a.eng.ruleStats()
	snap := a.eng.snap.Load()

	a.mu.Lock()
	s.RulesHash, s.RulesLoadedAt, s.RulesOrigin = a.rules.Hash, a.rulesLoadedAt, a.rulesOrigin
	for _, r := range a.rules.Rules {
		rs := RuleStatus{Name: r.Name, Pattern: r.Pattern, Files: r.Files, RuleStats: stats[r.Name]}
		if r.Absence {
			rs.Absent, rs.For = r.Absent, r.For.D().String()
			rs.LastSeenLine, rs.Firing = a.eng.absenceInfo(r.Name)
		}
		if w := r.SuppressWindow(); w > 0 {
			rs.Suppress = fmt.Sprintf("%d per %s", r.MaxAlerts(), w)
			if r.Suppress.GroupBy != "" {
				rs.Suppress += " by " + r.Suppress.GroupBy
			}
		}
		s.Rules = append(s.Rules, rs)
	}
	for p, h := range a.tailers {
		fs := FileStatus{Path: p, Open: h.t.opened.Load(), Offset: h.t.offset.Load(), Lines: h.t.lines.Load()}
		if e := h.t.lastErr.Load(); e != nil {
			fs.Error = *e
		}
		for _, r := range snap.byFile[p] {
			fs.Rules = append(fs.Rules, r.Name)
		}
		s.Files = append(s.Files, fs)
	}
	a.mu.Unlock()
	sort.Slice(s.Files, func(i, j int) bool { return s.Files[i].Path < s.Files[j].Path })

	s.Webhooks.Sent = a.notif.sender.Sent.Load()
	s.Webhooks.Failed = a.notif.sender.Failed.Load()
	s.Webhooks.Dropped = a.notif.sender.Dropped.Load()
	s.Webhooks.ViaDashboard = a.notif.routed.Load()
	s.Webhooks.Fallback = a.notif.fallback.Load()
	s.Webhooks.Silenced = a.notif.silencedCount.Load()
	s.Webhooks.Queued = a.notif.sender.Queued()
	s.SuppressKeys = a.eng.suppressKeys()
	if a.conn != nil {
		ds := a.conn.status()
		s.Dashboard = &ds
	}

	var ms runtime.MemStats
	runtime.ReadMemStats(&ms)
	s.Memory = MemoryStats{
		HeapAlloc:    ms.HeapAlloc,
		HeapInuse:    ms.HeapInuse,
		Sys:          ms.Sys,
		NumGC:        ms.NumGC,
		Goroutines:   runtime.NumGoroutine(),
		MemoryLimit:  debug.SetMemoryLimit(-1),
		StackInuse:   ms.StackInuse,
		HeapIdle:     ms.HeapIdle,
		HeapReleased: ms.HeapReleased,
		GCCPU:        ms.GCCPUFraction,
	}
	return s
}

func writeFileAtomic(path string, data []byte) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	if cur, err := os.ReadFile(path); err == nil && bytes.Equal(cur, data) {
		return nil
	}
	tmp, err := os.CreateTemp(filepath.Dir(path), ".rules-*.tmp")
	if err != nil {
		return err
	}
	defer os.Remove(tmp.Name())
	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Sync(); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(tmp.Name(), path)
}
