package agent

import (
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

type safeCollector struct {
	mu    sync.Mutex
	lines map[string][]string // by file
	order []string            // file of each line, in processing order
}

func (c *safeCollector) add(p string, l []byte) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.lines == nil {
		c.lines = map[string][]string{}
	}
	c.lines[p] = append(c.lines[p], string(l))
	c.order = append(c.order, p)
}

func (c *safeCollector) count(p string) int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return len(c.lines[p])
}

func schedTailer(path string, c *safeCollector) *tailer {
	return newTailer(path, 10*time.Millisecond, 1024, true, nil, c.add, func(string, error) {},
		slog.New(slog.NewTextHandler(io.Discard, nil)))
}

func waitUntil(t *testing.T, what string, cond func() bool) {
	t.Helper()
	for deadline := time.Now().Add(10 * time.Second); time.Now().Before(deadline); time.Sleep(5 * time.Millisecond) {
		if cond() {
			return
		}
	}
	t.Fatalf("timed out waiting for %s", what)
}

// A small file must not wait for a huge backlog in another file to finish,
// even with a single worker.
func TestSchedulerIsFair(t *testing.T) {
	dir := t.TempDir()
	big, small := filepath.Join(dir, "big.log"), filepath.Join(dir, "small.log")
	line := strings.Repeat("x", 99) + "\n"
	appendFile(t, big, strings.Repeat(line, 640000)) // 64 MB backlog
	appendFile(t, small, "")

	c := &safeCollector{}
	s := newScheduler(1, 10*time.Millisecond, 50*time.Millisecond)
	defer s.stop()
	s.add(schedTailer(big, c))
	s.add(schedTailer(small, c))
	waitUntil(t, "backlog started", func() bool { return c.count(big) > 0 })
	appendFile(t, small, "urgent\n")

	waitUntil(t, "small file line", func() bool { return c.count(small) == 1 })
	if n := c.count(big); n >= 640000 {
		t.Fatalf("small file was only read after the whole backlog (%d lines)", n)
	}
	waitUntil(t, "backlog finished", func() bool { return c.count(big) == 640000 })
}

func TestSchedulerManyFilesAndFinalRead(t *testing.T) {
	dir := t.TempDir()
	c := &safeCollector{}
	s := newScheduler(4, 10*time.Millisecond, 40*time.Millisecond)
	var paths []string
	for i := 0; i < 300; i++ {
		p := filepath.Join(dir, "f"+string(rune('a'+i%26))+strings.Repeat("_", i/26)+".log")
		appendFile(t, p, "first\n")
		paths = append(paths, p)
		s.add(schedTailer(p, c))
	}
	waitUntil(t, "all files read", func() bool {
		for _, p := range paths {
			if c.count(p) != 1 {
				return false
			}
		}
		return true
	})
	// Written right before stop: the final read must still pick it up.
	for _, p := range paths {
		appendFile(t, p, "last\n")
	}
	s.stop()
	for _, p := range paths {
		if c.count(p) != 2 {
			t.Fatalf("%s: %d lines, want 2 (final read on stop)", p, c.count(p))
		}
	}
}

func TestSchedulerRemoveClosesFile(t *testing.T) {
	p := filepath.Join(t.TempDir(), "a.log")
	appendFile(t, p, "x\n")
	c := &safeCollector{}
	s := newScheduler(2, 10*time.Millisecond, 20*time.Millisecond)
	defer s.stop()
	tl := schedTailer(p, c)
	s.add(tl)
	waitUntil(t, "opened", func() bool { return tl.opened.Load() })
	s.remove(tl)
	waitUntil(t, "closed", func() bool { return !tl.opened.Load() })
	appendFile(t, p, "after removal\n")
	time.Sleep(100 * time.Millisecond)
	if c.count(p) != 1 {
		t.Fatalf("removed file still read: %d lines", c.count(p))
	}
	if err := os.Remove(p); err != nil { // closed handle: deletable even on Windows
		t.Fatal(err)
	}
}
