package dashboard

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"encoding/json"
	"errors"
	"net"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"
)

// Sign-in and sessions. The browser only ever holds an HttpOnly session
// cookie, signed with a key kept in the data file; SSO tokens never reach it.
// Roles are resolved from role bindings on every request, so changes apply
// immediately. Scripts can use HTTP Basic with a local account.

const (
	sessionCookie = "grepalert_session"
	oidcCookie    = "grepalert_oidc"
)

type session struct {
	Identity
	IssuedAt  int64 `json:"iat"`
	ExpiresAt int64 `json:"exp"`
}

type ctxKey struct{}

// reqAuth is the caller of a request.
type reqAuth struct {
	id    Identity
	perms Perms
}

func authOf(r *http.Request) *reqAuth {
	if a, ok := r.Context().Value(ctxKey{}).(*reqAuth); ok {
		return a
	}
	return &reqAuth{}
}

// sign/verify implement "base64(json).base64(hmac)" tokens for cookies.
func (s *Server) sign(v any) string {
	raw, _ := json.Marshal(v)
	p := base64.RawURLEncoding.EncodeToString(raw)
	m := hmac.New(sha256.New, s.store.SessionKey())
	m.Write([]byte(p))
	return p + "." + base64.RawURLEncoding.EncodeToString(m.Sum(nil))
}

func (s *Server) verify(token string, v any) bool {
	p, sig, ok := strings.Cut(token, ".")
	if !ok {
		return false
	}
	m := hmac.New(sha256.New, s.store.SessionKey())
	m.Write([]byte(p))
	got, err := base64.RawURLEncoding.DecodeString(sig)
	if err != nil || !hmac.Equal(got, m.Sum(nil)) {
		return false
	}
	raw, err := base64.RawURLEncoding.DecodeString(p)
	return err == nil && json.Unmarshal(raw, v) == nil
}

func (s *Server) secureCookie(r *http.Request) bool {
	return s.cfg.Auth.CookieSecure || r.TLS != nil || strings.EqualFold(r.Header.Get("X-Forwarded-Proto"), "https")
}

func (s *Server) setSession(w http.ResponseWriter, r *http.Request, id Identity) {
	if len(id.Groups) > 50 {
		id.Groups = id.Groups[:50] // keep the cookie small
	}
	now := time.Now()
	sess := session{Identity: id, IssuedAt: now.Unix(), ExpiresAt: now.Add(s.cfg.Auth.SessionTTL.D()).Unix()}
	http.SetCookie(w, &http.Cookie{Name: sessionCookie, Value: s.sign(sess), Path: "/", HttpOnly: true,
		Secure: s.secureCookie(r), SameSite: http.SameSiteLaxMode, Expires: time.Unix(sess.ExpiresAt, 0)})
}

func clearCookie(w http.ResponseWriter, name string) {
	http.SetCookie(w, &http.Cookie{Name: name, Value: "", Path: "/", HttpOnly: true, MaxAge: -1})
}

// authConfigured reports whether any sign-in method exists. Without one the
// dashboard is open (a warning is logged and shown).
func (s *Server) authConfigured() bool {
	return s.cfg.Auth.Password != "" || s.cfg.Auth.OIDC != nil || len(s.store.Users()) > 0
}

func (s *Server) bootstrapAdmins() []string {
	admins := append([]string(nil), s.cfg.Auth.Admins...)
	if s.cfg.Auth.Username != "" {
		admins = append(admins, "user:"+s.cfg.Auth.Username) // the built-in local admin
	}
	return admins
}

// identify returns the caller of a request, if signed in.
func (s *Server) identify(r *http.Request) (Identity, bool) {
	if c, err := r.Cookie(sessionCookie); err == nil {
		var sess session
		if s.verify(c.Value, &sess) && time.Now().Unix() < sess.ExpiresAt &&
			sess.IssuedAt >= s.store.sessionsValidAfter("user:"+sess.User).Unix() {
			return sess.Identity, true
		}
	}
	if u, p, ok := r.BasicAuth(); ok {
		if !s.limiter.allow(clientIP(r)) {
			return Identity{}, false
		}
		if s.checkPassword(u, p) {
			return Identity{User: u, Method: "local"}, true
		}
		s.limiter.fail(clientIP(r))
		return Identity{}, false
	}
	if !s.authConfigured() {
		return Identity{User: "anonymous", Method: "anonymous"}, true
	}
	return Identity{}, false
}

// checkPassword verifies the built-in admin or a local user.
func (s *Server) checkPassword(user, pw string) bool {
	if cu, cp := s.cfg.Auth.Username, s.cfg.Auth.Password; cu != "" && strings.EqualFold(user, cu) {
		return subtle.ConstantTimeCompare([]byte(pw), []byte(cp)) == 1
	}
	return s.store.CheckLocal(user, pw)
}

// isPublic lists paths reachable without signing in.
func isPublic(path string) bool {
	switch path {
	case "/login", "/login.js", "/app.css", "/api/auth/config", "/auth/local", "/auth/oidc", "/auth/callback", "/healthz":
		return true
	}
	return false
}

// securityHeaders hardens every response. The UI loads only its own files
// (no inline scripts), so a strict Content-Security-Policy blocks injected
// scripts even if escaping were ever missed.
func securityHeaders(w http.ResponseWriter) {
	h := w.Header()
	h.Set("Content-Security-Policy", "default-src 'self'; script-src 'self'; style-src 'self' 'unsafe-inline'; "+
		"img-src 'self' data:; connect-src 'self'; frame-ancestors 'none'; base-uri 'none'; form-action 'self'")
	h.Set("X-Content-Type-Options", "nosniff")
	h.Set("X-Frame-Options", "DENY")
	h.Set("Referrer-Policy", "same-origin")
}

func (s *Server) auth(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		securityHeaders(w)
		path := r.URL.Path
		if strings.HasPrefix(path, "/agent/v1/") {
			next.ServeHTTP(w, r) // agents authenticate with their own credentials
			return
		}
		mutating := r.Method != http.MethodGet && r.Method != http.MethodHead
		// CSRF guard: browsers cannot send this header cross-site without a
		// CORS preflight, which we never approve. Applies to sign-in too.
		if mutating && r.Header.Get("X-Requested-With") != "grepalert" {
			http.Error(w, "missing X-Requested-With header", http.StatusForbidden)
			return
		}
		if isPublic(path) {
			next.ServeHTTP(w, r)
			return
		}
		id, ok := s.identify(r)
		if !ok {
			if strings.HasPrefix(path, "/api/") || r.Header.Get("Authorization") != "" {
				w.Header().Set("WWW-Authenticate", `Basic realm="grepalert"`)
				writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "sign in required"})
				return
			}
			http.Redirect(w, r, "/login?next="+url.QueryEscape(r.URL.RequestURI()), http.StatusFound)
			return
		}
		a := &reqAuth{id: id, perms: permsFor(id, s.store.Bindings(), s.bootstrapAdmins())}
		next.ServeHTTP(w, r.WithContext(context.WithValue(r.Context(), ctxKey{}, a)))
	})
}

// need checks that the caller has role on group, answering 403 if not.
// Refused changes are recorded in the audit log.
func (s *Server) need(w http.ResponseWriter, r *http.Request, role Role, group string) bool {
	a := authOf(r)
	if a.perms.Can(role, group) {
		return true
	}
	if r.Method != http.MethodGet {
		s.audit.record(r, "access.denied", r.Method+" "+r.URL.Path, group, "denied", "needs "+string(role)+scopeDesc(group), "")
	}
	writeJSON(w, http.StatusForbidden, map[string]string{"error": "you need the " + string(role) + " role" + scopeDesc(group) + " for this"})
	return false
}

func scopeDesc(group string) string {
	if group == "" {
		return " for all groups"
	}
	return " on group " + group
}

// ---- local sign-in ----

func (s *Server) handleLocalLogin(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Username string `json:"username"`
		Password string `json:"password"`
	}
	if !readJSON(w, r, &req) {
		return
	}
	ip := clientIP(r)
	if !s.limiter.allow(ip) {
		s.audit.recordAs(r, Identity{User: req.Username}, "login.local", req.Username, "", "denied", "too many failed attempts from "+ip, "")
		writeErr(w, http.StatusTooManyRequests, errors.New("too many failed attempts; try again in a few minutes"))
		return
	}
	if (s.cfg.Auth.Password == "" && len(s.store.Users()) == 0) || !s.checkPassword(req.Username, req.Password) {
		s.limiter.fail(ip)
		s.audit.recordAs(r, Identity{User: req.Username}, "login.local", req.Username, "", "failed", "wrong username or password", "")
		writeErr(w, http.StatusUnauthorized, errBadLogin)
		return
	}
	id := Identity{User: req.Username, Method: "local"}
	s.setSession(w, r, id)
	s.audit.recordAs(r, id, "login.local", req.Username, "", "ok", "", "")
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) handleLogout(w http.ResponseWriter, r *http.Request) {
	a := authOf(r)
	if r.URL.Query().Get("everywhere") == "1" && a.id.User != "" {
		s.store.InvalidateSessions("user:" + a.id.User)
	}
	clearCookie(w, sessionCookie)
	s.audit.record(r, "logout", a.id.User, "", "ok", "", "")
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) handleAuthConfig(w http.ResponseWriter, r *http.Request) {
	out := map[string]any{"local": s.cfg.Auth.Password != "" || len(s.store.Users()) > 0}
	if o := s.cfg.Auth.OIDC; o != nil {
		out["oidc"] = o.Name
	}
	writeJSON(w, http.StatusOK, out)
}

func (s *Server) handleMe(w http.ResponseWriter, r *http.Request) {
	a := authOf(r)
	writeJSON(w, http.StatusOK, map[string]any{"identity": a.id, "perms": a.perms,
		"open": a.id.Method == "anonymous", "oidc": s.cfg.Auth.OIDC != nil})
}

// ---- brute-force protection ----

// loginLimiter allows 10 failed sign-ins per IP per 5 minutes.
type loginLimiter struct {
	mu   sync.Mutex
	hits map[string][]time.Time
}

const (
	loginWindow   = 5 * time.Minute
	loginFailures = 10
)

func (l *loginLimiter) recent(ip string, now time.Time) []time.Time {
	keep := l.hits[ip][:0]
	for _, t := range l.hits[ip] {
		if now.Sub(t) < loginWindow {
			keep = append(keep, t)
		}
	}
	l.hits[ip] = keep
	return keep
}

func (l *loginLimiter) allow(ip string) bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	return len(l.recent(ip, time.Now())) < loginFailures
}

func (l *loginLimiter) fail(ip string) {
	l.mu.Lock()
	defer l.mu.Unlock()
	if len(l.hits) > 10000 { // bound memory under a distributed attack
		l.hits = map[string][]time.Time{}
	}
	l.hits[ip] = append(l.recent(ip, time.Now()), time.Now())
}

func clientIP(r *http.Request) string {
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return r.RemoteAddr
	}
	return host
}

// safeNext only allows redirects to paths on this dashboard.
func safeNext(next string) string {
	if next == "" || !strings.HasPrefix(next, "/") || strings.HasPrefix(next, "//") || strings.HasPrefix(next, "/\\") {
		return "/"
	}
	return next
}

// AuthConfigured reports whether any sign-in method is set up.
func (s *Server) AuthConfigured() bool { return s.authConfigured() }

// Close flushes and closes the audit log.
func (s *Server) Close() { s.audit.close() }
