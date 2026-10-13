package dashboard

import (
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base32"
	"encoding/hex"
	"errors"
	"fmt"
	"maps"
	"strings"
	"time"
)

// EnrollmentToken lets new agents register themselves. Only its hash is
// stored; the token itself is shown once when created.
type EnrollmentToken struct {
	ID        string    `json:"id"`
	Name      string    `json:"name"`
	Hash      string    `json:"hash"`
	Prefix    string    `json:"prefix"` // first characters, to recognise it
	CreatedAt time.Time `json:"created_at"`
	ExpiresAt time.Time `json:"expires_at,omitempty"`
	Revoked   bool      `json:"revoked,omitempty"`
	Uses      int       `json:"uses"`
	LastUsed  time.Time `json:"last_used,omitempty"`
}

const (
	enrollPrefix = "gae_" // grepalert enrollment
	secretPrefix = "gas_" // grepalert agent secret
)

var b32 = base32.StdEncoding.WithPadding(base32.NoPadding)

func randomToken(prefix string) string {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		panic(err) // crypto/rand never fails on supported platforms
	}
	return prefix + strings.ToLower(b32.EncodeToString(b))
}

func randomID() string {
	b := make([]byte, 8)
	rand.Read(b)
	return hex.EncodeToString(b)
}

func hashSecret(s string) string {
	sum := sha256.Sum256([]byte(s))
	return hex.EncodeToString(sum[:])
}

func sameHash(a, b string) bool { return subtle.ConstantTimeCompare([]byte(a), []byte(b)) == 1 }

// CreateToken stores a new enrollment token and returns it in clear text.
func (s *Store) CreateToken(name string, ttl time.Duration) (EnrollmentToken, string, error) {
	if err := checkName("token", name); err != nil {
		return EnrollmentToken{}, "", err
	}
	raw := randomToken(enrollPrefix)
	t := EnrollmentToken{ID: randomID(), Name: name, Hash: hashSecret(raw), Prefix: raw[:10], CreatedAt: time.Now()}
	if ttl > 0 {
		t.ExpiresAt = t.CreatedAt.Add(ttl)
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, x := range s.data.Tokens {
		if x.Name == name && !x.Revoked {
			return EnrollmentToken{}, "", fmt.Errorf("an active token named %q already exists", name)
		}
	}
	s.data.Tokens = append(s.data.Tokens, t)
	return t, raw, s.saveLocked()
}

func (s *Store) Tokens() []EnrollmentToken {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]EnrollmentToken(nil), s.data.Tokens...)
}

func (s *Store) RevokeToken(id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	for i := range s.data.Tokens {
		if s.data.Tokens[i].ID == id {
			s.data.Tokens[i].Revoked = true
			return s.saveLocked()
		}
	}
	return errors.New("token not found")
}

var errBadEnrollment = errors.New("invalid, expired or revoked enrollment token")

// useToken checks an enrollment token and records its use. staticToken is the
// optional token from the dashboard config (for automation).
func (s *Store) useTokenLocked(raw, staticToken string) (string, error) {
	if staticToken != "" && subtle.ConstantTimeCompare([]byte(raw), []byte(staticToken)) == 1 {
		return "config", nil
	}
	h := hashSecret(raw)
	for i := range s.data.Tokens {
		t := &s.data.Tokens[i]
		if !sameHash(t.Hash, h) {
			continue
		}
		if t.Revoked || (!t.ExpiresAt.IsZero() && time.Now().After(t.ExpiresAt)) {
			return "", errBadEnrollment
		}
		t.Uses++
		t.LastUsed = time.Now()
		return t.Name, nil
	}
	return "", errBadEnrollment
}

// EnrollRequest is what an agent sends to register.
type EnrollRequest struct {
	Token    string            `json:"token"`
	Name     string            `json:"name"`
	Hostname string            `json:"hostname"`
	Version  string            `json:"version"`
	Labels   map[string]string `json:"labels"`
}

// Enroll registers a connected agent and returns its ID and secret. An agent
// enrolling again under the same name (e.g. a rebuilt VM) gets new
// credentials and keeps its group; the old credentials stop working. A name
// used by a direct agent, or a revoked agent, is refused.
func (s *Store) Enroll(req EnrollRequest, staticToken string) (AgentRecord, string, error) {
	if err := checkName("agent", req.Name); err != nil {
		return AgentRecord{}, "", err
	}
	if err := CheckLabels(req.Labels); err != nil {
		return AgentRecord{}, "", err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	via, err := s.useTokenLocked(req.Token, staticToken)
	if err != nil {
		return AgentRecord{}, "", err
	}
	secret := randomToken(secretPrefix)
	rec := AgentRecord{Name: req.Name, Mode: "connected", ID: randomID(), SecretHash: hashSecret(secret),
		Labels: req.Labels, Hostname: req.Hostname, Version: req.Version, EnrolledVia: via, AddedAt: time.Now()}
	for i, x := range s.data.Agents {
		if x.Name != req.Name {
			continue
		}
		switch {
		case !x.Connected():
			return AgentRecord{}, "", fmt.Errorf("the name %q is used by an agent the dashboard connects to directly", req.Name)
		case x.Revoked:
			return AgentRecord{}, "", fmt.Errorf("agent %q was revoked; allow it again in the dashboard first", req.Name)
		}
		rec.Group, rec.AddedAt = x.Group, x.AddedAt
		s.data.Agents[i] = rec
		return rec, secret, s.saveLocked()
	}
	s.data.Agents = append(s.data.Agents, rec)
	return rec, secret, s.saveLocked()
}

var (
	errUnknownAgent = errors.New("unknown agent")
	errRevoked      = errors.New("agent access revoked")
)

// Authenticate checks a connected agent's "id.secret" credential.
func (s *Store) Authenticate(cred string) (AgentRecord, error) {
	id, secret, ok := strings.Cut(cred, ".")
	if !ok {
		return AgentRecord{}, errUnknownAgent
	}
	h := hashSecret(secret)
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, a := range s.data.Agents {
		if a.Connected() && a.ID == id {
			if !sameHash(a.SecretHash, h) {
				return AgentRecord{}, errUnknownAgent
			}
			if a.Revoked {
				return AgentRecord{}, errRevoked
			}
			return a, nil
		}
	}
	return AgentRecord{}, errUnknownAgent
}

// UpdateAgentInfo records what a connected agent reports about itself. It only
// writes the data file when something changed.
func (s *Store) UpdateAgentInfo(id, hostname, version string, labels map[string]string) (changed bool, err error) {
	if err := CheckLabels(labels); err != nil {
		return false, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	for i := range s.data.Agents {
		a := &s.data.Agents[i]
		if a.ID != id || !a.Connected() {
			continue
		}
		if a.Hostname == hostname && a.Version == version && maps.Equal(a.Labels, labels) {
			return false, nil
		}
		a.Hostname, a.Version, a.Labels = hostname, version, labels
		return true, s.saveLocked()
	}
	return false, errUnknownAgent
}

// AgentByID finds a connected agent by its ID.
func (s *Store) AgentByID(id string) (AgentRecord, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, a := range s.data.Agents {
		if a.Connected() && a.ID == id {
			return a, true
		}
	}
	return AgentRecord{}, false
}
