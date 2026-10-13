package dashboard

import (
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

// RuleVersion is one rule set a group has had. Every change (edit, git sync,
// rollback, aborted rollout) adds one, so any earlier version can be
// restored in one click.
type RuleVersion struct {
	Hash   string    `json:"hash"`
	Rules  string    `json:"rules"`
	Time   time.Time `json:"time"`
	Author string    `json:"author,omitempty"`
	// Source is how it came to be: created, edited, git, rollback,
	// rollout-abort or existing (from before versioning).
	Source string `json:"source"`
	Note   string `json:"note,omitempty"`
}

// maxVersions is how many versions are kept per group. Versions a group
// still needs (its current rules, and the stable version of a rollout) are
// kept even when older.
const maxVersions = 50

// addVersionLocked appends v unless it repeats the newest version, then drops
// the oldest versions the group no longer needs. Callers hold s.mu.
func (s *Store) addVersionLocked(group string, v RuleVersion) {
	if s.data.Versions == nil {
		s.data.Versions = map[string][]RuleVersion{}
	}
	vs := s.data.Versions[group]
	if n := len(vs); n > 0 && vs[n-1].Hash == v.Hash && v.Source != "rollback" && v.Source != "rollout-abort" {
		return
	}
	vs = append(vs, v)
	if len(vs) > maxVersions {
		keep := map[string]bool{}
		for _, g := range s.data.Groups {
			if g.Name == group {
				keep[g.RulesHash] = true
				if ro := g.RolloutState; ro != nil {
					keep[ro.StableHash] = true
				}
			}
		}
		drop := len(vs) - maxVersions
		out := vs[:0]
		for i, x := range vs {
			if i < len(vs)-maxVersions && drop > 0 && !keep[x.Hash] {
				drop--
				continue
			}
			out = append(out, x)
		}
		vs = out
	}
	s.data.Versions[group] = vs
}

// Versions returns a group's versions, newest first.
func (s *Store) Versions(group string) []RuleVersion {
	s.mu.Lock()
	defer s.mu.Unlock()
	vs := s.data.Versions[group]
	out := make([]RuleVersion, len(vs))
	for i, v := range vs {
		out[len(vs)-1-i] = v
	}
	return out
}

// Version returns the newest version of a group with the given hash.
func (s *Store) Version(group, hash string) (RuleVersion, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	vs := s.data.Versions[group]
	for i := len(vs) - 1; i >= 0; i-- {
		if vs[i].Hash == hash {
			return vs[i], true
		}
	}
	return RuleVersion{}, false
}

// ---- API ----

type versionView struct {
	Hash    string    `json:"hash"`
	Time    time.Time `json:"time"`
	Author  string    `json:"author,omitempty"`
	Source  string    `json:"source"`
	Note    string    `json:"note,omitempty"`
	Rules   int       `json:"rules"` // number of lines
	Current bool      `json:"current,omitempty"`
	Stable  bool      `json:"stable,omitempty"` // what agents outside a rollout run
}

func (s *Server) handleListVersions(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("name")
	g, ok := s.store.Group(name)
	if !ok {
		writeErr(w, http.StatusNotFound, errors.New("group not found"))
		return
	}
	out := []versionView{}
	seenCurrent, seenStable := false, false
	for _, v := range s.store.Versions(name) {
		vv := versionView{Hash: v.Hash, Time: v.Time, Author: v.Author, Source: v.Source, Note: v.Note,
			Rules: strings.Count(v.Rules, "\n")}
		if v.Hash == g.RulesHash && !seenCurrent {
			vv.Current, seenCurrent = true, true
		}
		if ro := g.RolloutState; ro != nil && ro.active() && v.Hash == ro.StableHash && !seenStable {
			vv.Stable, seenStable = true, true
		}
		out = append(out, vv)
	}
	writeJSON(w, http.StatusOK, out)
}

func (s *Server) handleGetVersion(w http.ResponseWriter, r *http.Request) {
	v, ok := s.store.Version(r.PathValue("name"), r.PathValue("hash"))
	if !ok {
		writeErr(w, http.StatusNotFound, errors.New("version not found"))
		return
	}
	w.Header().Set("Content-Type", "application/yaml")
	w.Header().Set("X-Rules-Hash", v.Hash)
	io.WriteString(w, v.Rules)
}

// handleRollback makes an earlier version the group's rules again, on every
// agent at once: a rollback is for when something is wrong, so it skips the
// staged rollout and stops any rollout in progress. A git group stops
// following git until someone syncs it again.
func (s *Server) handleRollback(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("name")
	var req struct {
		Hash string `json:"hash"`
	}
	if !readJSON(w, r, &req) {
		return
	}
	g, ok := s.store.Group(name)
	if !ok {
		writeErr(w, http.StatusNotFound, errors.New("group not found"))
		return
	}
	v, ok := s.store.Version(name, req.Hash)
	if !ok {
		writeErr(w, http.StatusNotFound, errors.New("version not found"))
		return
	}
	if _, err := validateForDashboard([]byte(v.Rules)); err != nil {
		writeErr(w, http.StatusUnprocessableEntity, fmt.Errorf("that version no longer validates: %w", err))
		return
	}
	actor := authOf(r).id.User
	note := fmt.Sprintf("rolled back from %s to %s", orNone(g.RulesHash), v.Hash)
	err := s.store.SetGroupRules(name, []byte(v.Rules), RuleVersion{Author: actor, Source: "rollback", Note: note}, func(g *GroupRecord) {
		if ro := g.RolloutState; ro != nil && ro.active() {
			ro.finish("aborted", "rolled back to "+v.Hash+" by "+actor)
		}
		if g.Source == "git" {
			g.GitPaused = "rolled back to " + v.Hash + " by " + actor + "; sync from git to follow git again"
		}
	})
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err)
		return
	}
	s.audit.record(r, "group.rules.rollback", name, name, "ok", note, lineDiff(g.Rules, v.Rules))
	if err := s.syncGroup(r.Context(), name, "rollback", false); err != nil && !errors.Is(err, errSyncing) {
		writeErr(w, http.StatusBadGateway, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"hash": v.Hash, "state": s.snapshotState(name)})
}
