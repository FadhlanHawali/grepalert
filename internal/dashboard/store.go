package dashboard

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"sync"
	"time"

	"github.com/fadhlanhawali/grepalert/internal/rules"
)

// AgentRecord is an agent the dashboard is connected to.
type AgentRecord struct {
	Name               string    `json:"name"`
	URL                string    `json:"url"`
	Token              string    `json:"token,omitempty"` // may be ${ENV}
	Group              string    `json:"group,omitempty"`
	InsecureSkipVerify bool      `json:"insecure_skip_verify,omitempty"`
	AddedAt            time.Time `json:"added_at"`
}

// GitSource is where a git-backed group reads its rules from.
type GitSource struct {
	Provider string `json:"provider"` // github, gitlab or url
	// BaseURL overrides the API endpoint for GitHub Enterprise
	// (https://ghe.example.com/api/v3) or self-hosted GitLab.
	BaseURL string `json:"base_url,omitempty"`
	Repo    string `json:"repo,omitempty"` // owner/name or group/project
	Ref     string `json:"ref,omitempty"`  // branch, tag or commit
	Path    string `json:"path"`           // file in the repo; for "url" the full URL
	Token   string `json:"token,omitempty"`
	// Interval is how often to re-fetch, e.g. "5m". Empty means manual only.
	Interval string `json:"interval,omitempty"`
}

// GroupRecord is a set of agents sharing one rule set.
type GroupRecord struct {
	Name        string     `json:"name"`
	Description string     `json:"description,omitempty"`
	Source      string     `json:"source"` // "manual" or "git"
	Git         *GitSource `json:"git,omitempty"`
	// Rules is the group's rule set: edited in the dashboard (manual) or the
	// last version fetched from git.
	Rules          string    `json:"rules,omitempty"`
	RulesHash      string    `json:"rules_hash,omitempty"`
	RulesUpdatedAt time.Time `json:"rules_updated_at,omitempty"`
	CreatedAt      time.Time `json:"created_at"`
}

type storeData struct {
	Version int           `json:"version"`
	Agents  []AgentRecord `json:"agents"`
	Groups  []GroupRecord `json:"groups"`
}

// Store persists agents and groups to a JSON file. It holds tokens, so the
// file is written with 0600 permissions.
type Store struct {
	path string
	mu   sync.Mutex
	data storeData
}

var validName = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]{0,63}$`)

func checkName(kind, name string) error {
	if !validName.MatchString(name) {
		return fmt.Errorf("%s name must be 1-64 characters: letters, digits, '.', '_' or '-'", kind)
	}
	return nil
}

func OpenStore(path string) (*Store, error) {
	s := &Store{path: path, data: storeData{Version: 1}}
	raw, err := os.ReadFile(path)
	if errors.Is(err, fs.ErrNotExist) {
		return s, nil
	}
	if err != nil {
		return nil, err
	}
	if err := json.Unmarshal(raw, &s.data); err != nil {
		return nil, fmt.Errorf("parse %s: %w", path, err)
	}
	return s, nil
}

func (s *Store) saveLocked() error {
	raw, err := json.MarshalIndent(s.data, "", "  ")
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(s.path), 0o755); err != nil {
		return err
	}
	// CreateTemp creates the file with 0600, which the rename keeps.
	tmp, err := os.CreateTemp(filepath.Dir(s.path), ".dashboard-*.tmp")
	if err != nil {
		return err
	}
	defer os.Remove(tmp.Name())
	if _, err := tmp.Write(raw); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(tmp.Name(), s.path)
}

func (s *Store) Agents() []AgentRecord {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := append([]AgentRecord(nil), s.data.Agents...)
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out
}

func (s *Store) Agent(name string) (AgentRecord, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, a := range s.data.Agents {
		if a.Name == name {
			return a, true
		}
	}
	return AgentRecord{}, false
}

// AddAgent stores a new agent.
func (s *Store) AddAgent(a AgentRecord) error {
	if err := checkName("agent", a.Name); err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, x := range s.data.Agents {
		if x.Name == a.Name {
			return fmt.Errorf("an agent named %q already exists", a.Name)
		}
	}
	if a.Group != "" && !s.hasGroupLocked(a.Group) {
		return fmt.Errorf("group %q does not exist", a.Group)
	}
	a.AddedAt = time.Now()
	s.data.Agents = append(s.data.Agents, a)
	return s.saveLocked()
}

// UpdateAgent replaces an agent's settings. An empty token keeps the old one.
func (s *Store) UpdateAgent(a AgentRecord) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if a.Group != "" && !s.hasGroupLocked(a.Group) {
		return fmt.Errorf("group %q does not exist", a.Group)
	}
	for i, x := range s.data.Agents {
		if x.Name == a.Name {
			if a.Token == "" {
				a.Token = x.Token
			}
			a.AddedAt = x.AddedAt
			s.data.Agents[i] = a
			return s.saveLocked()
		}
	}
	return fmt.Errorf("agent %q not found", a.Name)
}

func (s *Store) DeleteAgent(name string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	for i, x := range s.data.Agents {
		if x.Name == name {
			s.data.Agents = append(s.data.Agents[:i], s.data.Agents[i+1:]...)
			return s.saveLocked()
		}
	}
	return fmt.Errorf("agent %q not found", name)
}

func (s *Store) Groups() []GroupRecord {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := append([]GroupRecord(nil), s.data.Groups...)
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out
}

func (s *Store) Group(name string) (GroupRecord, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, g := range s.data.Groups {
		if g.Name == name {
			return g, true
		}
	}
	return GroupRecord{}, false
}

func (s *Store) hasGroupLocked(name string) bool {
	for _, g := range s.data.Groups {
		if g.Name == name {
			return true
		}
	}
	return false
}

// Members returns the agents in a group.
func (s *Store) Members(group string) []AgentRecord {
	var out []AgentRecord
	for _, a := range s.Agents() {
		if a.Group == group {
			out = append(out, a)
		}
	}
	return out
}

func validateGroup(g *GroupRecord) error {
	if err := checkName("group", g.Name); err != nil {
		return err
	}
	switch g.Source {
	case "manual":
		g.Git = nil
	case "git":
		if g.Git == nil {
			return errors.New("git settings are required for a git group")
		}
		gs := g.Git
		switch gs.Provider {
		case "github", "gitlab":
			if gs.Repo == "" || gs.Path == "" {
				return errors.New("repository and file path are required")
			}
		case "url":
			if gs.Path == "" {
				return errors.New("the URL of the rules file is required")
			}
		default:
			return errors.New("provider must be github, gitlab or url")
		}
		if gs.Ref == "" && gs.Provider != "url" {
			gs.Ref = "main"
		}
		if gs.Interval != "" {
			d, err := time.ParseDuration(gs.Interval)
			if err != nil || d < 10*time.Second {
				return errors.New("sync interval must be a duration of at least 10s, e.g. 5m")
			}
		}
	default:
		return errors.New(`source must be "manual" or "git"`)
	}
	return nil
}

func (s *Store) AddGroup(g GroupRecord) error {
	if err := validateGroup(&g); err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.hasGroupLocked(g.Name) {
		return fmt.Errorf("a group named %q already exists", g.Name)
	}
	g.CreatedAt = time.Now()
	if g.Rules != "" {
		g.RulesHash = rules.Hash([]byte(g.Rules))
		g.RulesUpdatedAt = time.Now()
	}
	s.data.Groups = append(s.data.Groups, g)
	return s.saveLocked()
}

// UpdateGroup changes a group's description and source. Rules are kept; an
// empty git token keeps the previous one.
func (s *Store) UpdateGroup(g GroupRecord) error {
	if err := validateGroup(&g); err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	for i, x := range s.data.Groups {
		if x.Name != g.Name {
			continue
		}
		if g.Git != nil && g.Git.Token == "" && x.Git != nil {
			g.Git.Token = x.Git.Token
		}
		g.Rules, g.RulesHash, g.RulesUpdatedAt, g.CreatedAt = x.Rules, x.RulesHash, x.RulesUpdatedAt, x.CreatedAt
		s.data.Groups[i] = g
		return s.saveLocked()
	}
	return fmt.Errorf("group %q not found", g.Name)
}

// SetGroupRules stores a new rule set for a group.
func (s *Store) SetGroupRules(name string, raw []byte) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	for i, g := range s.data.Groups {
		if g.Name == name {
			s.data.Groups[i].Rules = string(raw)
			s.data.Groups[i].RulesHash = rules.Hash(raw)
			s.data.Groups[i].RulesUpdatedAt = time.Now()
			return s.saveLocked()
		}
	}
	return fmt.Errorf("group %q not found", name)
}

// DeleteGroup removes a group. Its agents become ungrouped and keep running
// the rules they have.
func (s *Store) DeleteGroup(name string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	for i, g := range s.data.Groups {
		if g.Name == name {
			s.data.Groups = append(s.data.Groups[:i], s.data.Groups[i+1:]...)
			for j := range s.data.Agents {
				if s.data.Agents[j].Group == name {
					s.data.Agents[j].Group = ""
				}
			}
			return s.saveLocked()
		}
	}
	return fmt.Errorf("group %q not found", name)
}
