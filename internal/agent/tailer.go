package agent

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"io"
	"io/fs"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"time"
)

// LineFunc receives each complete line. The slice is only valid during the
// call: it points into a reused buffer, so the hot path does not allocate.
type LineFunc func(path string, line []byte)

// Position is a saved read position, persisted so restarts resume where the
// agent stopped instead of skipping (or replaying) lines.
type Position struct {
	// Offset is just past the last complete line handed to the rules.
	Offset int64 `json:"offset"`
	// ID is the OS file identity (inode or Windows file index), if available.
	ID string `json:"id,omitempty"`
	// FP is a SHA-256 of the first FPLen bytes. It tells "same file" apart from
	// "a new file now lives at this path" even when IDs are reused or missing.
	FP    string `json:"fp"`
	FPLen int    `json:"fp_len"`
}

// fpSize is how much of a file's head is fingerprinted.
const fpSize = 1024

// tailer follows a single file by polling. Polling (instead of inotify) keeps
// it portable and makes rotation/truncation handling explicit and predictable.
//
// Memory is fixed per file: one read buffer plus one line buffer capped at
// maxLine bytes. Longer lines are truncated, never buffered whole.
type tailer struct {
	path      string
	poll      time.Duration
	maxLine   int
	fromStart bool
	resume    *Position // saved position to continue from on first open (tailer goroutine only)
	saved     *Position // immutable copy of resume, readable from any goroutine
	onLine    LineFunc
	onError   func(path string, err error)
	log       *slog.Logger

	f        *os.File
	fi       os.FileInfo
	opened   atomic.Bool
	offset   atomic.Int64
	lines    atomic.Uint64
	lastErr  atomic.Pointer[string]
	readBuf  []byte
	lineBuf  []byte
	overflow bool // current line exceeded maxLine; drop until newline

	// committed is the offset just past the last complete line. Unlike
	// offset it excludes a partially written trailing line, so resuming from
	// it never loses the start of a line.
	committed int64
	id        string
	fp        string
	fpLen     int
	pos       atomic.Pointer[Position]
}

func newTailer(path string, poll time.Duration, maxLine int, fromStart bool, resume *Position,
	onLine LineFunc, onError func(string, error), log *slog.Logger) *tailer {
	return &tailer{
		path:      path,
		poll:      poll,
		maxLine:   maxLine,
		fromStart: fromStart,
		resume:    resume,
		saved:     resume,
		onLine:    onLine,
		onError:   onError,
		log:       log,
		readBuf:   make([]byte, 32*1024),
		lineBuf:   make([]byte, 0, 1024),
	}
}

func (t *tailer) run(ctx context.Context) {
	defer t.close()
	ticker := time.NewTicker(t.poll)
	defer ticker.Stop()
	for {
		t.step()
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
}

// position returns the position to persist, or nil if there is nothing to save.
// It is safe to call from any goroutine.
func (t *tailer) position() *Position {
	if p := t.pos.Load(); p != nil {
		return p
	}
	// Not opened yet (e.g. the file was rotated away while we were down and
	// has not been recreated): keep the saved position for the next restart.
	return t.saved
}

func (t *tailer) step() {
	if t.f == nil {
		if !t.open() {
			return
		}
	}
	t.drain()

	// At EOF: detect rotation (path now points to a different file) and
	// truncation (file shrank below our offset).
	cur, err := os.Stat(t.path)
	switch {
	case errors.Is(err, fs.ErrNotExist):
		// Rotated away and not yet recreated; keep the old handle until the
		// new file shows up so trailing writes are not lost.
		return
	case err != nil:
		t.fail(err)
		return
	}
	if !os.SameFile(cur, t.fi) {
		t.drain() // pick up anything written to the old file just before rotation
		t.close()
		t.fromStart = true // a freshly rotated file must be read from the top
		if t.open() {
			t.drain()
		}
		return
	}
	if cur.Size() < t.offset.Load() {
		if _, err := t.f.Seek(0, io.SeekStart); err != nil {
			t.fail(err)
			t.close()
			return
		}
		t.setStart(0)
		t.drain()
	}
}

// fail reports err unless it repeats the previous error, so a file that stays
// unreadable produces one event instead of one per poll.
func (t *tailer) fail(err error) {
	msg := err.Error()
	if p := t.lastErr.Load(); p != nil && *p == msg {
		return
	}
	t.lastErr.Store(&msg)
	t.onError(t.path, err)
}

func (t *tailer) open() bool {
	f, err := openShared(t.path)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			// Not created yet: whatever gets written once it exists is new,
			// so read it from the start.
			t.fromStart = true
		} else {
			t.fail(err)
		}
		return false
	}
	fi, err := f.Stat()
	if err != nil {
		f.Close()
		t.fail(err)
		return false
	}
	t.f, t.fi = f, fi
	t.id = fileID(f)

	var off int64
	switch {
	case t.resume != nil:
		r := *t.resume
		t.resume = nil
		if t.sameFile(f, fi.Size(), t.id, r) {
			off = r.Offset
			t.log.Info("resuming from saved position", "offset", off)
		} else {
			// The file was rotated or truncated while we were stopped. Finish
			// the old file if it is still around, then read the new one in full.
			t.catchUpRotated(r)
			t.log.Info("file changed while stopped, reading it from the start")
		}
	case !t.fromStart:
		off = fi.Size()
	}
	if _, err := f.Seek(off, io.SeekStart); err != nil {
		t.close()
		t.fail(err)
		return false
	}
	t.opened.Store(true)
	t.lastErr.Store(nil)
	t.setStart(off)
	// Any file we open after the first one (rotation, re-creation) is new
	// content and must be read in full.
	t.fromStart = true
	return true
}

// setStart resets read state to begin at off in the current file.
func (t *tailer) setStart(off int64) {
	t.offset.Store(off)
	t.committed = off
	t.fp, t.fpLen = "", 0
	t.resetLine()
	t.publish()
}

// sameFile reports whether f (of the given size and id) is the file that the
// saved position r refers to.
func (t *tailer) sameFile(f *os.File, size int64, id string, r Position) bool {
	if size < r.Offset {
		return false
	}
	if r.ID != "" && id != "" && r.ID != id {
		return false
	}
	fp, ok := fingerprint(f, r.FPLen)
	return ok && fp == r.FP
}

// catchUpRotated looks for the file r referred to under a rotated name next to
// path (app.log.1, app.log-20261012, …) and emits the lines written to it
// after r.Offset, i.e. those written between our shutdown and the rotation.
func (t *tailer) catchUpRotated(r Position) {
	dir, base := filepath.Split(t.path)
	entries, err := os.ReadDir(filepath.Clean(dir))
	if err != nil {
		return
	}
	checked := 0
	for _, e := range entries {
		name := e.Name()
		if name == base || !strings.HasPrefix(name, base) || !e.Type().IsRegular() || isCompressed(name) {
			continue
		}
		if checked++; checked > 50 {
			return
		}
		p := filepath.Join(dir, name)
		f, err := openShared(p)
		if err != nil {
			continue
		}
		fi, err := f.Stat()
		// The ID is not required to match: copytruncate leaves a copy with a
		// new ID but the same content.
		if err == nil && fi.Size() >= r.Offset {
			if fp, ok := fingerprint(f, r.FPLen); ok && fp == r.FP {
				if _, err := f.Seek(r.Offset, io.SeekStart); err == nil {
					t.log.Info("catching up on rotated file", "rotated", p, "offset", r.Offset)
					t.resetLine()
					t.readAll(f)
					if len(t.lineBuf) > 0 {
						t.emit() // the rotated file is complete; flush its last line
					}
				}
				f.Close()
				return
			}
		}
		f.Close()
	}
}

func isCompressed(name string) bool {
	switch strings.ToLower(filepath.Ext(name)) {
	case ".gz", ".bz2", ".xz", ".zst", ".zip", ".lz4":
		return true
	}
	return false
}

// fingerprint hashes the first n bytes of f without moving its read offset.
func fingerprint(f *os.File, n int) (string, bool) {
	var buf [fpSize]byte
	if n > fpSize {
		n = fpSize
	}
	if _, err := f.ReadAt(buf[:n], 0); err != nil && !(errors.Is(err, io.EOF) && n == 0) {
		return "", false
	}
	sum := sha256.Sum256(buf[:n])
	return hex.EncodeToString(sum[:]), true
}

// publish makes the current position visible to the state saver.
func (t *tailer) publish() {
	if t.f == nil {
		return
	}
	// Fingerprint as much of the head as has been committed, up to fpSize.
	if want := min(t.committed, fpSize); int64(t.fpLen) < want || t.fp == "" {
		if fp, ok := fingerprint(t.f, int(want)); ok {
			t.fp, t.fpLen = fp, int(want)
		}
	}
	if p := t.pos.Load(); p != nil && p.Offset == t.committed && p.FP == t.fp && p.ID == t.id {
		return
	}
	t.pos.Store(&Position{Offset: t.committed, ID: t.id, FP: t.fp, FPLen: t.fpLen})
}

func (t *tailer) drain() {
	t.readAll(t.f)
	t.publish()
}

func (t *tailer) readAll(f *os.File) {
	for {
		n, err := f.Read(t.readBuf)
		if n > 0 && f == t.f {
			t.consume(t.readBuf[:n], t.offset.Add(int64(n))-int64(n))
		} else if n > 0 {
			t.consume(t.readBuf[:n], -1)
		}
		if err != nil || n == 0 {
			if err != nil && !errors.Is(err, io.EOF) {
				t.fail(err)
			}
			return
		}
	}
}

// consume splits chunk into lines. base is the file offset of chunk[0], or -1
// when reading a file other than the one being tailed.
func (t *tailer) consume(chunk []byte, base int64) {
	for done := 0; done < len(chunk); {
		i := bytes.IndexByte(chunk[done:], '\n')
		if i < 0 {
			t.appendLine(chunk[done:])
			return
		}
		t.appendLine(chunk[done : done+i])
		done += i + 1
		t.emit()
		if base >= 0 {
			t.committed = base + int64(done)
		}
	}
}

func (t *tailer) appendLine(b []byte) {
	if t.overflow {
		return
	}
	if room := t.maxLine - len(t.lineBuf); len(b) > room {
		b = b[:room]
		t.overflow = true
	}
	t.lineBuf = append(t.lineBuf, b...)
}

func (t *tailer) emit() {
	line := bytes.TrimSuffix(t.lineBuf, []byte{'\r'})
	t.lines.Add(1)
	t.onLine(t.path, line)
	t.resetLine()
}

func (t *tailer) resetLine() {
	t.lineBuf = t.lineBuf[:0]
	t.overflow = false
	// If a pathological line grew the buffer, give the memory back.
	if cap(t.lineBuf) > 64*1024 {
		t.lineBuf = make([]byte, 0, 1024)
	}
}

func (t *tailer) close() {
	if t.f != nil {
		t.f.Close()
		t.f, t.fi = nil, nil
		t.opened.Store(false)
	}
}
