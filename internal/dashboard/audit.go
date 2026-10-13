package dashboard

import (
	"bufio"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

// The audit log records who changed what, when and from where: sign-ins,
// agents, groups, rules (with a diff), silences, settings, tokens, users and
// role bindings, plus refused attempts. It is an append-only JSON-lines file
// (0600) that rotates at auditMaxSize keeping auditKeep old files, so it can
// be shipped to a SIEM; the newest entries are also kept in memory for the UI.

const (
	auditMaxSize = 20 << 20
	auditKeep    = 5
	auditMemory  = 2000
	maxDiff      = 64 << 10
)

type AuditEntry struct {
	Time   time.Time `json:"time"`
	Actor  string    `json:"actor"`
	Name   string    `json:"name,omitempty"`
	Method string    `json:"method,omitempty"` // oidc, local, anonymous, agent, git, system
	IP     string    `json:"ip,omitempty"`
	Action string    `json:"action"` // e.g. group.rules.update
	Target string    `json:"target,omitempty"`
	Group  string    `json:"group,omitempty"`
	Result string    `json:"result"` // ok, failed, denied
	Detail string    `json:"detail,omitempty"`
	Diff   string    `json:"diff,omitempty"`
}

type auditLog struct {
	path string
	log  *slog.Logger

	mu   sync.Mutex
	f    *os.File
	size int64
	mem  []AuditEntry // ring
	next int
	full bool
}

func openAudit(path string, log *slog.Logger) *auditLog {
	l := &auditLog{path: path, log: log, mem: make([]AuditEntry, auditMemory)}
	l.loadTail()
	if err := l.open(); err != nil {
		log.Error("audit log: cannot open file, keeping entries in memory only", "path", path, "err", err)
	}
	return l
}

func (l *auditLog) open() error {
	if l.path == "" {
		return nil
	}
	if err := os.MkdirAll(filepath.Dir(l.path), 0o755); err != nil {
		return err
	}
	f, err := os.OpenFile(l.path, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o600)
	if err != nil {
		return err
	}
	fi, _ := f.Stat()
	l.f, l.size = f, fi.Size()
	return nil
}

// loadTail fills the in-memory view from the current file.
func (l *auditLog) loadTail() {
	f, err := os.Open(l.path)
	if err != nil {
		return
	}
	defer f.Close()
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 64*1024), 2*maxDiff)
	for sc.Scan() {
		var e AuditEntry
		if json.Unmarshal(sc.Bytes(), &e) == nil {
			l.addMem(e)
		}
	}
}

func (l *auditLog) addMem(e AuditEntry) {
	l.mem[l.next] = e
	l.next = (l.next + 1) % len(l.mem)
	if l.next == 0 {
		l.full = true
	}
}

func (l *auditLog) rotateLocked() {
	if l.f == nil {
		return
	}
	l.f.Close()
	for i := auditKeep - 1; i >= 1; i-- {
		os.Rename(fmt.Sprintf("%s.%d", l.path, i), fmt.Sprintf("%s.%d", l.path, i+1))
	}
	os.Remove(fmt.Sprintf("%s.%d", l.path, auditKeep+1))
	os.Rename(l.path, l.path+".1")
	if err := l.open(); err != nil {
		l.f = nil
		l.log.Error("audit log: cannot reopen after rotation", "err", err)
	}
}

func (l *auditLog) write(e AuditEntry) {
	if len(e.Diff) > maxDiff {
		e.Diff = e.Diff[:maxDiff] + "\n… (diff truncated)"
	}
	line, _ := json.Marshal(e)
	line = append(line, '\n')
	l.mu.Lock()
	defer l.mu.Unlock()
	l.addMem(e)
	if l.f == nil {
		return
	}
	if l.size+int64(len(line)) > auditMaxSize {
		l.rotateLocked()
		if l.f == nil {
			return
		}
	}
	n, err := l.f.Write(line)
	l.size += int64(n)
	if err != nil {
		l.log.Error("audit log write failed", "err", err)
	}
}

// record logs an action by the caller of r.
func (l *auditLog) record(r *http.Request, action, target, group, result, detail, diff string) {
	l.recordAs(r, authOf(r).id, action, target, group, result, detail, diff)
}

func (l *auditLog) recordAs(r *http.Request, id Identity, action, target, group, result, detail, diff string) {
	e := AuditEntry{Time: time.Now().UTC(), Actor: id.User, Name: id.Name, Method: id.Method, Action: action,
		Target: target, Group: group, Result: result, Detail: detail, Diff: diff}
	if r != nil {
		e.IP = clientIP(r)
	}
	if e.Actor == "" {
		e.Actor = "unknown"
	}
	l.write(e)
}

// system logs an action not caused by a person (git sync, enrollment).
func (l *auditLog) system(actor, method, action, target, group, detail, diff string) {
	l.write(AuditEntry{Time: time.Now().UTC(), Actor: actor, Method: method, Action: action, Target: target,
		Group: group, Result: "ok", Detail: detail, Diff: diff})
}

// Entries returns up to limit entries, newest first, filtered by a
// case-insensitive substring over actor, action, target and group.
func (l *auditLog) Entries(limit int, filter string) []AuditEntry {
	filter = strings.ToLower(filter)
	l.mu.Lock()
	defer l.mu.Unlock()
	n := l.next
	if l.full {
		n = len(l.mem)
	}
	out := []AuditEntry{}
	for i := 0; i < n && len(out) < limit; i++ {
		e := l.mem[(l.next-1-i+len(l.mem))%len(l.mem)]
		if filter != "" && !strings.Contains(strings.ToLower(e.Actor+" "+e.Action+" "+e.Target+" "+e.Group+" "+e.Result), filter) {
			continue
		}
		out = append(out, e)
	}
	return out
}

func (l *auditLog) close() {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.f != nil {
		l.f.Close()
		l.f = nil
	}
}

// ---- diff ----

// lineDiff returns a unified-style diff of two texts with up to 2 lines of
// context, or "" if they are equal. Large inputs get a summary instead.
func lineDiff(a, b string) string {
	if a == b {
		return ""
	}
	x, y := strings.Split(a, "\n"), strings.Split(b, "\n")
	if len(x)*len(y) > 4_000_000 {
		return fmt.Sprintf("(large change: %d → %d lines)", len(x), len(y))
	}
	// LCS table.
	lcs := make([][]int, len(x)+1)
	for i := range lcs {
		lcs[i] = make([]int, len(y)+1)
	}
	for i := len(x) - 1; i >= 0; i-- {
		for j := len(y) - 1; j >= 0; j-- {
			if x[i] == y[j] {
				lcs[i][j] = lcs[i+1][j+1] + 1
			} else {
				lcs[i][j] = max(lcs[i+1][j], lcs[i][j+1])
			}
		}
	}
	type op struct {
		kind byte
		text string
	}
	var ops []op
	i, j := 0, 0
	for i < len(x) || j < len(y) {
		switch {
		case i < len(x) && j < len(y) && x[i] == y[j]:
			ops = append(ops, op{' ', x[i]})
			i, j = i+1, j+1
		case j < len(y) && (i == len(x) || lcs[i][j+1] > lcs[i+1][j]): // removals before additions
			ops = append(ops, op{'+', y[j]})
			j++
		default:
			ops = append(ops, op{'-', x[i]})
			i++
		}
	}
	const ctx = 2
	var sb strings.Builder
	lastPrinted := -1
	for k, o := range ops {
		if o.kind == ' ' {
			near := false
			for d := -ctx; d <= ctx; d++ {
				if k+d >= 0 && k+d < len(ops) && ops[k+d].kind != ' ' {
					near = true
				}
			}
			if !near {
				continue
			}
		}
		if lastPrinted >= 0 && k > lastPrinted+1 {
			sb.WriteString("…\n")
		}
		sb.WriteByte(o.kind)
		sb.WriteByte(' ')
		sb.WriteString(o.text)
		sb.WriteByte('\n')
		lastPrinted = k
	}
	return sb.String()
}
