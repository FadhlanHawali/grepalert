package agent

import (
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

type collector struct{ lines []string }

func (c *collector) add(_ string, l []byte) { c.lines = append(c.lines, string(l)) }

func newTestTailer(t *testing.T, path string, fromStart bool, maxLine int) (*tailer, *collector) {
	return newResumeTailer(t, path, fromStart, maxLine, nil)
}

func newResumeTailer(t *testing.T, path string, fromStart bool, maxLine int, resume *Position) (*tailer, *collector) {
	t.Helper()
	c := &collector{}
	tl := newTailer(path, time.Hour, maxLine, fromStart, resume, c.add,
		func(p string, err error) { t.Logf("tail error %s: %v", p, err) }, slog.New(slog.NewTextHandler(io.Discard, nil)))
	t.Cleanup(tl.close)
	return tl, c
}

func appendFile(t *testing.T, path, s string) {
	t.Helper()
	f, err := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	if _, err := f.WriteString(s); err != nil {
		t.Fatal(err)
	}
}

func expectLines(t *testing.T, c *collector, want ...string) {
	t.Helper()
	if strings.Join(c.lines, "|") != strings.Join(want, "|") {
		t.Fatalf("lines = %q, want %q", c.lines, want)
	}
	c.lines = nil
}

func TestTailerStartsAtEndAndReadsPartialLines(t *testing.T) {
	p := filepath.Join(t.TempDir(), "app.log")
	appendFile(t, p, "old line\n")
	tl, c := newTestTailer(t, p, false, 1024)

	tl.step()
	expectLines(t, c)

	appendFile(t, p, "new 1\r\nnew ")
	tl.step()
	expectLines(t, c, "new 1")

	appendFile(t, p, "2\n")
	tl.step()
	expectLines(t, c, "new 2")
}

func TestTailerTruncation(t *testing.T) {
	p := filepath.Join(t.TempDir(), "app.log")
	appendFile(t, p, "a\nb\n")
	tl, c := newTestTailer(t, p, true, 1024)
	tl.step()
	expectLines(t, c, "a", "b")

	if err := os.Truncate(p, 0); err != nil {
		t.Fatal(err)
	}
	appendFile(t, p, "c\n")
	tl.step()
	expectLines(t, c, "c")
}

func TestTailerRotation(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "app.log")
	appendFile(t, p, "")
	tl, c := newTestTailer(t, p, false, 1024)
	tl.step()

	appendFile(t, p, "before\n")
	if err := os.Rename(p, p+".1"); err != nil {
		t.Fatal(err)
	}
	// Written to the old file after the rename but before we noticed.
	appendFile(t, p+".1", "late\n")
	tl.step() // file missing: keep reading old handle
	expectLines(t, c, "before", "late")

	appendFile(t, p, "after\n")
	tl.step()
	expectLines(t, c, "after")
}

func TestTailerFileCreatedLater(t *testing.T) {
	p := filepath.Join(t.TempDir(), "later.log")
	tl, c := newTestTailer(t, p, false, 1024)
	tl.step()
	appendFile(t, p, "first\n")
	tl.step()
	expectLines(t, c, "first")
}

func TestTailerLongLineIsTruncated(t *testing.T) {
	p := filepath.Join(t.TempDir(), "app.log")
	appendFile(t, p, "")
	tl, c := newTestTailer(t, p, false, 10)
	tl.step()
	appendFile(t, p, strings.Repeat("x", 100000)+"\nshort\n")
	tl.step()
	expectLines(t, c, strings.Repeat("x", 10), "short")
	if cap(tl.lineBuf) > 64*1024 {
		t.Fatalf("line buffer grew to %d", cap(tl.lineBuf))
	}
}

// restart simulates stopping a tailer and starting a new one from its saved position.
func restart(t *testing.T, tl *tailer, path string) (*tailer, *collector) {
	t.Helper()
	pos := tl.position()
	if pos == nil {
		t.Fatal("no position saved")
	}
	tl.close()
	return newResumeTailer(t, path, false, 1024, pos)
}

func TestResumeAfterRestart(t *testing.T) {
	p := filepath.Join(t.TempDir(), "app.log")
	appendFile(t, p, "old\n")
	tl, c := newTestTailer(t, p, false, 1024)
	tl.step()
	appendFile(t, p, "a\nb\npart")
	tl.step()
	expectLines(t, c, "a", "b")

	// Written while the agent is down, including the rest of a partial line.
	appendFile(t, p, "ial\nc\n")
	tl2, c2 := restart(t, tl, p)
	tl2.step()
	expectLines(t, c2, "partial", "c")
}

func TestResumeAfterRenameRotation(t *testing.T) {
	p := filepath.Join(t.TempDir(), "app.log")
	appendFile(t, p, "")
	tl, c := newTestTailer(t, p, false, 1024)
	tl.step()
	appendFile(t, p, "seen\n")
	tl.step()
	expectLines(t, c, "seen")
	pos := tl.position()
	tl.close()

	// While stopped: more lines, then rotation, then lines in the new file.
	appendFile(t, p, "missed 1\nmissed 2")
	if err := os.Rename(p, p+".1"); err != nil {
		t.Fatal(err)
	}
	appendFile(t, p+".1.gz", "not a candidate\n")
	appendFile(t, p, "new 1\n")

	tl2, c2 := newResumeTailer(t, p, false, 1024, pos)
	tl2.step()
	expectLines(t, c2, "missed 1", "missed 2", "new 1")
}

func TestResumeAfterCopyTruncate(t *testing.T) {
	p := filepath.Join(t.TempDir(), "app.log")
	appendFile(t, p, "")
	tl, c := newTestTailer(t, p, false, 1024)
	tl.step()
	appendFile(t, p, "seen\n")
	tl.step()
	expectLines(t, c, "seen")
	pos := tl.position()
	tl.close()

	appendFile(t, p, "missed\n")
	data, err := os.ReadFile(p)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p+"-20261012", data, 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Truncate(p, 0); err != nil {
		t.Fatal(err)
	}
	appendFile(t, p, "after\n")

	tl2, c2 := newResumeTailer(t, p, false, 1024, pos)
	tl2.step()
	expectLines(t, c2, "missed", "after")
}

func TestResumeWhenFileReplacedAndOldOneGone(t *testing.T) {
	p := filepath.Join(t.TempDir(), "app.log")
	appendFile(t, p, "")
	tl, _ := newTestTailer(t, p, false, 1024)
	tl.step()
	appendFile(t, p, "original content that is long enough\n")
	tl.step()
	pos := tl.position()
	tl.close()

	if err := os.Remove(p); err != nil {
		t.Fatal(err)
	}
	appendFile(t, p, "brand new file with more content than before, so it is larger\n")
	tl2, c2 := newResumeTailer(t, p, false, 1024, pos)
	tl2.step()
	expectLines(t, c2, "brand new file with more content than before, so it is larger")
}

func TestPositionKeptWhileFileMissing(t *testing.T) {
	p := filepath.Join(t.TempDir(), "gone.log")
	pos := &Position{Offset: 10, FP: "x", FPLen: 10}
	tl, _ := newResumeTailer(t, p, false, 1024, pos)
	tl.step()
	if got := tl.position(); got == nil || *got != *pos {
		t.Fatalf("position = %+v, want saved position kept", got)
	}
}
