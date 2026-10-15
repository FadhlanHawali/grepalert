package agent

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
)

// stateFile is the on-disk format of saved read positions.
type stateFile struct {
	Version int                 `json:"version"`
	Files   map[string]Position `json:"files"`
}

func (a *Agent) stateEnabled() bool { return a.cfg.StateFile != "off" }

func loadState(path string) (map[string]Position, error) {
	raw, err := os.ReadFile(path)
	if errors.Is(err, fs.ErrNotExist) {
		return map[string]Position{}, nil
	}
	if err != nil {
		return map[string]Position{}, err
	}
	var s stateFile
	if err := json.Unmarshal(raw, &s); err != nil {
		return map[string]Position{}, fmt.Errorf("parse %s: %w", path, err)
	}
	if s.Files == nil {
		s.Files = map[string]Position{}
	}
	return s.Files, nil
}

// saveState writes the read position of every tailer. When handles is nil the
// currently running tailers are used. Positions only advance once a line has
// been matched (and any alert queued), so a crash replays at most the lines
// since the last save rather than losing them.
func (a *Agent) saveState(handles []*tailerHandle) {
	if !a.stateEnabled() {
		return
	}
	s := stateFile{Version: 1, Files: map[string]Position{}}
	a.mu.Lock()
	if handles == nil {
		for _, h := range a.tailers {
			handles = append(handles, h)
		}
	}
	a.mu.Unlock()
	for _, h := range handles {
		if p := h.t.position(); p != nil {
			s.Files[h.t.path] = *p
		}
	}
	for path, p := range a.kube.positions() {
		s.Files[path] = p
	}
	raw, err := json.MarshalIndent(s, "", "  ")
	if err != nil {
		return
	}
	if bytes.Equal(raw, a.lastState) {
		return
	}
	if err := writeFileAtomic(a.cfg.StateFile, raw); err != nil {
		a.log.Error("save state", "path", a.cfg.StateFile, "err", err)
		return
	}
	a.lastState = raw
}
