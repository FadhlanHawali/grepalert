package dashboard

import (
	"crypto/rand"
	"errors"
	"fmt"
	"slices"
	"strings"
	"time"

	"golang.org/x/crypto/bcrypt"
)

// Role is what a user may do. Admin is always global; viewer and editor can
// be granted for all groups or for one group.
type Role string

const (
	RoleNone   Role = ""
	RoleViewer Role = "viewer" // read everything in scope
	RoleEditor Role = "editor" // also edit group rules, sync, silence
	RoleAdmin  Role = "admin"  // also agents, groups, enrollment, settings, access, audit
)

func (r Role) level() int {
	switch r {
	case RoleViewer:
		return 1
	case RoleEditor:
		return 2
	case RoleAdmin:
		return 3
	}
	return 0
}

// LocalUser is a username/password account managed in the dashboard.
type LocalUser struct {
	Username     string    `json:"username"`
	PasswordHash string    `json:"password_hash"`
	CreatedAt    time.Time `json:"created_at"`
}

// RoleBinding grants Role to a subject, for one group or (Group "") all.
type RoleBinding struct {
	ID string `json:"id"`
	// Subject is "user:<email or username>" or "group:<identity-provider group>".
	Subject   string    `json:"subject"`
	Role      Role      `json:"role"`
	Group     string    `json:"group,omitempty"`
	CreatedAt time.Time `json:"created_at"`
	CreatedBy string    `json:"created_by,omitempty"`
}

// Identity is a signed-in user.
type Identity struct {
	// User is the email (SSO) or username (local) that role bindings match.
	User   string   `json:"user"`
	Name   string   `json:"name,omitempty"`
	Method string   `json:"method"` // oidc, local, anonymous
	Groups []string `json:"groups,omitempty"`
}

// Perms is what an identity may do, resolved from role bindings on every
// request, so changes apply immediately.
type Perms struct {
	Admin  bool            `json:"admin"`
	Global Role            `json:"global,omitempty"`
	Groups map[string]Role `json:"groups,omitempty"`
}

// Can reports whether the identity has at least role on group. group "" is
// for things outside any group (ungrouped agents, all-groups silences),
// which only global roles cover.
func (p Perms) Can(role Role, group string) bool {
	if p.Admin {
		return true
	}
	if role == RoleAdmin {
		return false
	}
	if p.Global.level() >= role.level() {
		return true
	}
	return group != "" && p.Groups[group].level() >= role.level()
}

// Any reports whether the identity can see anything at all.
func (p Perms) Any() bool { return p.Admin || p.Global != RoleNone || len(p.Groups) > 0 }

// permsFor resolves an identity's permissions. bootstrapAdmins come from the
// config file and always win.
func permsFor(id Identity, bindings []RoleBinding, bootstrapAdmins []string) Perms {
	p := Perms{Groups: map[string]Role{}}
	if id.Method == "anonymous" {
		p.Admin = true
		return p
	}
	matches := func(subject string) bool {
		kind, val, _ := strings.Cut(subject, ":")
		switch kind {
		case "user":
			return strings.EqualFold(val, id.User)
		case "group":
			return slices.Contains(id.Groups, val)
		}
		return strings.EqualFold(subject, id.User) // bare name in config
	}
	for _, a := range bootstrapAdmins {
		if matches(a) {
			p.Admin = true
		}
	}
	for _, b := range bindings {
		if !matches(b.Subject) {
			continue
		}
		switch {
		case b.Role == RoleAdmin:
			p.Admin = true
		case b.Group == "":
			if b.Role.level() > p.Global.level() {
				p.Global = b.Role
			}
		case b.Role.level() > p.Groups[b.Group].level():
			p.Groups[b.Group] = b.Role
		}
	}
	return p
}

// ---- store ----

var errBadLogin = errors.New("invalid username or password")

func (s *Store) Users() []LocalUser {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]LocalUser, len(s.data.Users))
	for i, u := range s.data.Users {
		out[i] = LocalUser{Username: u.Username, CreatedAt: u.CreatedAt} // never the hash
	}
	return out
}

func checkPassword(pw string) error {
	if len(pw) < 12 {
		return errors.New("passwords must be at least 12 characters")
	}
	return nil
}

// SetUser creates a local user or changes its password.
func (s *Store) SetUser(username, password string) (created bool, err error) {
	if err := checkName("user", username); err != nil {
		return false, err
	}
	if err := checkPassword(password); err != nil {
		return false, err
	}
	hash, err := bcrypt.GenerateFromPassword([]byte(password), bcrypt.DefaultCost)
	if err != nil {
		return false, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	for i := range s.data.Users {
		if strings.EqualFold(s.data.Users[i].Username, username) {
			s.data.Users[i].PasswordHash = string(hash)
			s.invalidateLocked("user:" + username)
			return false, s.saveLocked()
		}
	}
	s.data.Users = append(s.data.Users, LocalUser{Username: username, PasswordHash: string(hash), CreatedAt: time.Now()})
	return true, s.saveLocked()
}

func (s *Store) DeleteUser(username string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	for i, u := range s.data.Users {
		if strings.EqualFold(u.Username, username) {
			s.data.Users = append(s.data.Users[:i], s.data.Users[i+1:]...)
			s.invalidateLocked("user:" + username)
			return s.saveLocked()
		}
	}
	return errors.New("user not found")
}

// dummyHash makes failed logins for unknown users take as long as for known
// ones, so usernames cannot be probed by timing.
var dummyHash, _ = bcrypt.GenerateFromPassword([]byte("grepalert-timing-equaliser"), bcrypt.DefaultCost)

// CheckLocal verifies a local user's password.
func (s *Store) CheckLocal(username, password string) bool {
	s.mu.Lock()
	hash := dummyHash
	found := false
	for _, u := range s.data.Users {
		if strings.EqualFold(u.Username, username) {
			hash, found = []byte(u.PasswordHash), true
		}
	}
	s.mu.Unlock()
	err := bcrypt.CompareHashAndPassword(hash, []byte(password))
	return found && err == nil
}

func (s *Store) Bindings() []RoleBinding {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]RoleBinding(nil), s.data.Bindings...)
}

func (s *Store) AddBinding(b RoleBinding) (RoleBinding, error) {
	kind, val, ok := strings.Cut(b.Subject, ":")
	if !ok || (kind != "user" && kind != "group") || strings.TrimSpace(val) == "" {
		return RoleBinding{}, errors.New(`subject must be "user:<email or username>" or "group:<group name>"`)
	}
	switch b.Role {
	case RoleViewer, RoleEditor:
	case RoleAdmin:
		if b.Group != "" {
			return RoleBinding{}, errors.New("admin is always for all groups")
		}
	default:
		return RoleBinding{}, errors.New("role must be viewer, editor or admin")
	}
	if b.Group != "" {
		if _, ok := s.Group(b.Group); !ok {
			return RoleBinding{}, fmt.Errorf("group %q does not exist", b.Group)
		}
	}
	b.ID, b.CreatedAt = randomID(), time.Now()
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, x := range s.data.Bindings {
		if strings.EqualFold(x.Subject, b.Subject) && x.Role == b.Role && x.Group == b.Group {
			return RoleBinding{}, errors.New("this binding already exists")
		}
	}
	s.data.Bindings = append(s.data.Bindings, b)
	return b, s.saveLocked()
}

func (s *Store) DeleteBinding(id string) (RoleBinding, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for i, b := range s.data.Bindings {
		if b.ID == id {
			s.data.Bindings = append(s.data.Bindings[:i], s.data.Bindings[i+1:]...)
			return b, s.saveLocked()
		}
	}
	return RoleBinding{}, errors.New("binding not found")
}

// SessionKey returns the key that signs session cookies, creating it once.
func (s *Store) SessionKey() []byte {
	s.mu.Lock()
	defer s.mu.Unlock()
	if len(s.data.SessionKey) < 32 {
		k := make([]byte, 32)
		rand.Read(k)
		s.data.SessionKey = k
		s.saveLocked()
	}
	return s.data.SessionKey
}

// invalidateLocked ends all sessions of a subject issued before now (e.g.
// password change, user deleted, "sign out everywhere").
func (s *Store) invalidateLocked(subject string) {
	if s.data.ValidAfter == nil {
		s.data.ValidAfter = map[string]time.Time{}
	}
	s.data.ValidAfter[strings.ToLower(subject)] = time.Now()
}

func (s *Store) InvalidateSessions(subject string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.invalidateLocked(subject)
	return s.saveLocked()
}

func (s *Store) sessionsValidAfter(subject string) time.Time {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.data.ValidAfter[strings.ToLower(subject)]
}
