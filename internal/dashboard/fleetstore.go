package dashboard

import (
	"errors"
	"fmt"
	"slices"
	"time"
)

// FleetSettings controls how a group's alerts are deduplicated across agents.
type FleetSettings struct {
	// Disabled forwards every agent's alert as is.
	Disabled bool `json:"disabled,omitempty"`
	// Wait is how long the first alert for a key waits to gather other hosts
	// reporting the same thing, so one alert can say "on 50 hosts".
	Wait string `json:"wait,omitempty"`
	// Window is how long further alerts for the same key are held back after
	// that; when it closes, a summary is sent if any were.
	Window string `json:"window,omitempty"`
}

const (
	defaultFleetWait   = 5 * time.Second
	defaultFleetWindow = 5 * time.Minute
)

func (f FleetSettings) validate() error {
	for _, v := range []struct{ name, val string }{{"fleet wait", f.Wait}, {"fleet window", f.Window}} {
		if v.val == "" {
			continue
		}
		if d, err := time.ParseDuration(v.val); err != nil || d < 0 {
			return fmt.Errorf("%s must be a duration like 5s or 10m", v.name)
		}
	}
	return nil
}

func (f FleetSettings) waitD() time.Duration   { return parseOr(f.Wait, defaultFleetWait) }
func (f FleetSettings) windowD() time.Duration { return parseOr(f.Window, defaultFleetWindow) }

func parseOr(s string, def time.Duration) time.Duration {
	if d, err := time.ParseDuration(s); err == nil && s != "" {
		return d
	}
	return def
}

// Silence mutes alerts for a while (maintenance windows). Empty Group, Agents
// or Rules match everything. Alerts still show in the history as silenced.
type Silence struct {
	ID        string    `json:"id"`
	Group     string    `json:"group,omitempty"`
	Agents    []string  `json:"agents,omitempty"`
	Rules     []string  `json:"rules,omitempty"`
	StartsAt  time.Time `json:"starts_at"`
	EndsAt    time.Time `json:"ends_at"`
	Reason    string    `json:"reason,omitempty"`
	CreatedAt time.Time `json:"created_at"`
}

// AgentDownRule is the rule name agent-down/up alerts use, so silences can
// target them.
const AgentDownRule = "agent-down"

func (s Silence) Active(now time.Time) bool { return !now.Before(s.StartsAt) && now.Before(s.EndsAt) }

// Matches reports whether the silence covers an alert of rule on agent in group.
func (s Silence) Matches(group, agent, rule string) bool {
	return (s.Group == "" || s.Group == group) &&
		(len(s.Agents) == 0 || slices.Contains(s.Agents, agent)) &&
		(len(s.Rules) == 0 || slices.Contains(s.Rules, rule))
}

// Settings are dashboard-wide options edited in the UI.
type Settings struct {
	// OpsWebhook receives agent-down/up alerts (groups can override the URL).
	OpsWebhook OpsWebhook `json:"ops_webhook"`
	// AgentDownAfter is how long an agent must be unreachable before an
	// agent-down alert is sent (default 2m).
	AgentDownAfter string `json:"agent_down_after,omitempty"`
}

type OpsWebhook struct {
	URL      string `json:"url,omitempty"` // may be ${ENV}
	Template string `json:"template,omitempty"`
}

const defaultAgentDownAfter = 2 * time.Minute

func (st Settings) agentDownAfter() time.Duration {
	return parseOr(st.AgentDownAfter, defaultAgentDownAfter)
}

func (s *Store) Settings() Settings {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.data.Settings
}

func (s *Store) SetSettings(st Settings) error {
	if st.AgentDownAfter != "" {
		if d, err := time.ParseDuration(st.AgentDownAfter); err != nil || d < 10*time.Second {
			return errors.New("agent-down delay must be a duration of at least 10s, e.g. 2m")
		}
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.data.Settings = st
	return s.saveLocked()
}

// AddSilence stores a silence; expired silences older than a week are pruned.
func (s *Store) AddSilence(sl Silence) (Silence, error) {
	if !sl.EndsAt.After(sl.StartsAt) {
		return Silence{}, errors.New("the silence must end after it starts")
	}
	if sl.Group != "" {
		if _, ok := s.Group(sl.Group); !ok {
			return Silence{}, fmt.Errorf("group %q does not exist", sl.Group)
		}
	}
	sl.ID, sl.CreatedAt = randomID(), time.Now()
	s.mu.Lock()
	defer s.mu.Unlock()
	keep := s.data.Silences[:0]
	for _, x := range s.data.Silences {
		if time.Since(x.EndsAt) < 7*24*time.Hour {
			keep = append(keep, x)
		}
	}
	s.data.Silences = append(keep, sl)
	return sl, s.saveLocked()
}

func (s *Store) Silences() []Silence {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]Silence(nil), s.data.Silences...)
}

// ExpireSilence ends a silence now.
func (s *Store) ExpireSilence(id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	for i := range s.data.Silences {
		if s.data.Silences[i].ID == id {
			if now := time.Now(); s.data.Silences[i].EndsAt.After(now) {
				s.data.Silences[i].EndsAt = now
			}
			return s.saveLocked()
		}
	}
	return errors.New("silence not found")
}

// activeSilence returns the first active silence covering the alert, if any.
func (s *Store) activeSilence(group, agent, rule string) (Silence, bool) {
	now := time.Now()
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, sl := range s.data.Silences {
		if sl.Active(now) && sl.Matches(group, agent, rule) {
			return sl, true
		}
	}
	return Silence{}, false
}
