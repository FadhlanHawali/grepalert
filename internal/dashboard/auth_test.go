package dashboard

import (
	"crypto/rand"
	"crypto/rsa"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"net/url"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/go-jose/go-jose/v4"
	"github.com/go-jose/go-jose/v4/jwt"
)

// authHarness is a dashboard with sign-in configured.
type authHarness struct {
	t   *testing.T
	srv *Server
	ts  *httptest.Server
}

func newAuthHarness(t *testing.T, mutate func(*Config)) *authHarness {
	t.Helper()
	dir := t.TempDir()
	store, err := OpenStore(filepath.Join(dir, "d.json"))
	if err != nil {
		t.Fatal(err)
	}
	cfg := Config{AuditFile: filepath.Join(dir, "audit.log"),
		Auth: Auth{Username: "root", Password: "correct horse battery staple"}}
	if mutate != nil {
		mutate(&cfg)
	}
	cfg.setDefaults()
	s := New(cfg, store, slog.New(slog.NewTextHandler(io.Discard, nil)))
	ts := httptest.NewServer(s.Handler())
	t.Cleanup(ts.Close)
	t.Cleanup(s.Close)
	return &authHarness{t: t, srv: s, ts: ts}
}

// client is a browser-like client with its own cookie jar.
func (h *authHarness) client() *http.Client {
	jar, _ := cookiejar.New(nil)
	return &http.Client{Jar: jar, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
}

func (h *authHarness) req(c *http.Client, method, path string, body any) (int, string) {
	h.t.Helper()
	var rd io.Reader
	switch b := body.(type) {
	case nil:
	case string:
		rd = strings.NewReader(b)
	default:
		raw, _ := json.Marshal(b)
		rd = strings.NewReader(string(raw))
	}
	req, _ := http.NewRequest(method, h.ts.URL+path, rd)
	req.Header.Set("X-Requested-With", "grepalert")
	resp, err := c.Do(req)
	if err != nil {
		h.t.Fatal(err)
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(resp.Body)
	return resp.StatusCode, string(raw)
}

func (h *authHarness) login(user, pw string) *http.Client {
	h.t.Helper()
	c := h.client()
	if code, body := h.req(c, "POST", "/auth/local", map[string]string{"username": user, "password": pw}); code != http.StatusNoContent {
		h.t.Fatalf("login %s: HTTP %d %s", user, code, body)
	}
	return c
}

func TestOpenModeWithoutAuth(t *testing.T) {
	h := newAuthHarness(t, func(c *Config) { c.Auth = Auth{} })
	code, body := h.req(h.client(), "GET", "/api/me", nil)
	if code != 200 || !strings.Contains(body, `"open":true`) || !strings.Contains(body, `"admin":true`) {
		t.Fatalf("open mode: %d %s", code, body)
	}
}

func TestLocalLoginAndSessions(t *testing.T) {
	h := newAuthHarness(t, nil)
	anon := h.client()

	// Not signed in: API 401, page redirects to /login; login page is public
	// and every response carries a strict CSP.
	if code, _ := h.req(anon, "GET", "/api/overview", nil); code != http.StatusUnauthorized {
		t.Fatalf("overview without sign-in: %d", code)
	}
	resp, _ := anon.Get(h.ts.URL + "/")
	if resp.StatusCode != http.StatusFound || !strings.HasPrefix(resp.Header.Get("Location"), "/login") {
		t.Fatalf("index without sign-in: %d %s", resp.StatusCode, resp.Header.Get("Location"))
	}
	resp, _ = anon.Get(h.ts.URL + "/login")
	if resp.StatusCode != 200 || !strings.Contains(resp.Header.Get("Content-Security-Policy"), "script-src 'self'") {
		t.Fatalf("login page: %d csp=%q", resp.StatusCode, resp.Header.Get("Content-Security-Policy"))
	}

	// Wrong password: refused and audited.
	if code, _ := h.req(anon, "POST", "/auth/local", map[string]string{"username": "root", "password": "nope"}); code != http.StatusUnauthorized {
		t.Fatalf("wrong password: %d", code)
	}
	c := h.login("root", "correct horse battery staple")
	if code, body := h.req(c, "GET", "/api/me", nil); code != 200 || !strings.Contains(body, `"user":"root"`) || !strings.Contains(body, `"admin":true`) {
		t.Fatalf("me: %d %s", code, body)
	}

	// Session cookie is HttpOnly + SameSite; tampering is rejected.
	u, _ := url.Parse(h.ts.URL)
	var sess *http.Cookie
	for _, ck := range c.Jar.Cookies(u) {
		if ck.Name == sessionCookie {
			sess = ck
		}
	}
	if sess == nil {
		t.Fatal("no session cookie")
	}
	forged := h.client()
	payload, sig, _ := strings.Cut(sess.Value, ".")
	raw, _ := json.Marshal(session{Identity: Identity{User: "root", Method: "local"}, IssuedAt: time.Now().Unix(), ExpiresAt: time.Now().Add(time.Hour).Unix()})
	_ = payload
	forged.Jar.SetCookies(u, []*http.Cookie{{Name: sessionCookie, Value: b64(raw) + "." + sig}})
	if code, _ := h.req(forged, "GET", "/api/me", nil); code != http.StatusUnauthorized {
		t.Fatalf("forged cookie accepted: %d", code)
	}

	// Basic auth for scripts.
	req, _ := http.NewRequest("GET", h.ts.URL+"/api/overview", nil)
	req.SetBasicAuth("root", "correct horse battery staple")
	if resp, _ := http.DefaultClient.Do(req); resp.StatusCode != 200 {
		t.Fatalf("basic auth: %d", resp.StatusCode)
	}

	// Local users; a password reset ends their sessions.
	if code, body := h.req(c, "PUT", "/api/users/alice", map[string]string{"password": "short"}); code != 400 {
		t.Fatalf("weak password accepted: %d %s", code, body)
	}
	h.req(c, "PUT", "/api/users/alice", map[string]string{"password": "alice-password-1"})
	alice := h.login("alice", "alice-password-1")
	time.Sleep(1100 * time.Millisecond) // sessions are compared at 1s resolution
	h.req(c, "PUT", "/api/users/alice", map[string]string{"password": "alice-password-2"})
	if code, _ := h.req(alice, "GET", "/api/me", nil); code != http.StatusUnauthorized {
		t.Fatal("old session survived a password reset")
	}

	// Sign out.
	h.req(c, "POST", "/auth/logout", nil)
	if code, _ := h.req(c, "GET", "/api/me", nil); code != http.StatusUnauthorized {
		t.Fatal("still signed in after logout")
	}

	// Audit trail.
	actions := map[string]int{}
	for _, e := range h.srv.audit.Entries(100, "") {
		actions[e.Action+":"+e.Result]++
	}
	for _, want := range []string{"login.local:failed", "login.local:ok", "user.create:ok", "user.password_reset:ok", "logout:ok"} {
		if actions[want] == 0 {
			t.Errorf("audit missing %s (have %v)", want, actions)
		}
	}
}

func b64(b []byte) string { return base64.RawURLEncoding.EncodeToString(b) }

func TestLoginRateLimit(t *testing.T) {
	h := newAuthHarness(t, nil)
	c := h.client()
	for i := 0; i < loginFailures; i++ {
		h.req(c, "POST", "/auth/local", map[string]string{"username": "root", "password": "wrong"})
	}
	// Even the right password is refused while the limit applies.
	code, _ := h.req(c, "POST", "/auth/local", map[string]string{"username": "root", "password": "correct horse battery staple"})
	if code != http.StatusTooManyRequests {
		t.Fatalf("got %d, want 429 after %d failures", code, loginFailures)
	}
}

func TestRBAC(t *testing.T) {
	h := newAuthHarness(t, nil)
	root := h.login("root", "correct horse battery staple")
	for _, g := range []string{"web", "db"} {
		h.req(root, "POST", "/api/groups", map[string]any{"name": g, "source": "manual", "rules": groupRules})
	}
	for _, u := range []string{"vera", "eddie", "gwen"} {
		h.req(root, "PUT", "/api/users/"+u, map[string]string{"password": u + "-password-123"})
	}
	grant := func(subject, role, group string) {
		if code, body := h.req(root, "POST", "/api/access", map[string]string{"subject": subject, "role": role, "group": group}); code != 200 {
			t.Fatalf("grant: %d %s", code, body)
		}
	}
	grant("user:vera", "viewer", "web")  // web viewer
	grant("user:eddie", "editor", "web") // web editor
	grant("user:gwen", "viewer", "")     // global viewer

	vera, eddie, gwen := h.login("vera", "vera-password-123"), h.login("eddie", "eddie-password-123"), h.login("gwen", "gwen-password-123")
	newRules := strings.Replace(groupRules, "ERROR", "FATAL", 1)
	cases := []struct {
		who          string
		c            *http.Client
		method, path string
		body         any
		want         int
	}{
		{"vera", vera, "GET", "/api/groups/web/rules", nil, 200},
		{"vera", vera, "GET", "/api/groups/db/rules", nil, 403},
		{"vera", vera, "PUT", "/api/groups/web/rules", newRules, 403},
		{"vera", vera, "GET", "/api/settings", nil, 403},
		{"eddie", eddie, "PUT", "/api/groups/web/rules", newRules, 200},
		{"eddie", eddie, "PUT", "/api/groups/db/rules", newRules, 403},
		{"eddie", eddie, "POST", "/api/silences", map[string]string{"group": "web", "duration": "1h"}, 200},
		{"eddie", eddie, "POST", "/api/silences", map[string]string{"group": "", "duration": "1h"}, 403},
		{"eddie", eddie, "POST", "/api/groups", map[string]any{"name": "x", "source": "manual"}, 403},
		{"eddie", eddie, "DELETE", "/api/groups/web", nil, 403},
		{"eddie", eddie, "POST", "/api/tokens", map[string]string{"name": "t"}, 403},
		{"eddie", eddie, "GET", "/api/audit", nil, 403},
		{"eddie", eddie, "POST", "/api/access", map[string]string{"subject": "user:eddie", "role": "admin"}, 403},
		{"gwen", gwen, "GET", "/api/groups/db/rules", nil, 200},
		{"gwen", gwen, "PUT", "/api/groups/db/rules", newRules, 403},
		{"root", root, "GET", "/api/audit", nil, 200},
	}
	for _, c := range cases {
		if code, body := h.req(c.c, c.method, c.path, c.body); code != c.want {
			t.Errorf("%s %s %s: HTTP %d, want %d (%s)", c.who, c.method, c.path, code, c.want, body)
		}
	}

	// Overview only shows what is in scope.
	var ov struct {
		Groups []groupView `json:"groups"`
	}
	_, body := h.req(vera, "GET", "/api/overview", nil)
	json.Unmarshal([]byte(body), &ov)
	if len(ov.Groups) != 1 || ov.Groups[0].Name != "web" {
		t.Fatalf("vera sees groups %+v, want only web", ov.Groups)
	}

	// Audit: the rules change carries a diff and who made it; refusals are logged.
	var sawDiff, sawDenied bool
	for _, e := range h.srv.audit.Entries(200, "") {
		if e.Action == "group.rules.update" && e.Actor == "eddie" && strings.Contains(e.Diff, "+     pattern: FATAL") {
			sawDiff = true
		}
		if e.Action == "access.denied" && e.Actor == "eddie" && e.Result == "denied" {
			sawDenied = true
		}
	}
	if !sawDiff || !sawDenied {
		t.Fatalf("audit diff=%v denied=%v", sawDiff, sawDenied)
	}
}

// ---- OIDC ----

// fakeIdP is a minimal OpenID Connect provider for tests.
type fakeIdP struct {
	srv    *httptest.Server
	key    *rsa.PrivateKey
	mu     sync.Mutex
	codes  map[string]string // code -> nonce
	claims map[string]any
	issuer string // public issuer; defaults to the server URL
}

func newFakeIdP(t *testing.T, claims map[string]any) *fakeIdP {
	key, _ := rsa.GenerateKey(rand.Reader, 2048)
	p := &fakeIdP{key: key, codes: map[string]string{}, claims: claims}
	mux := http.NewServeMux()
	p.srv = httptest.NewServer(mux)
	t.Cleanup(p.srv.Close)
	iss := func() string {
		if p.issuer != "" {
			return p.issuer
		}
		return p.srv.URL
	}
	base := p.srv.URL
	mux.HandleFunc("/.well-known/openid-configuration", func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(map[string]any{"issuer": iss(), "authorization_endpoint": base + "/authorize",
			"token_endpoint": base + "/token", "jwks_uri": base + "/jwks", "end_session_endpoint": base + "/logout", "id_token_signing_alg_values_supported": []string{"RS256"}})
	})
	mux.HandleFunc("/jwks", func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(jose.JSONWebKeySet{Keys: []jose.JSONWebKey{{Key: &key.PublicKey, KeyID: "k1", Algorithm: "RS256", Use: "sig"}}})
	})
	mux.HandleFunc("/authorize", func(w http.ResponseWriter, r *http.Request) {
		q := r.URL.Query()
		if q.Get("code_challenge_method") != "S256" || q.Get("code_challenge") == "" {
			http.Error(w, "PKCE required", 400)
			return
		}
		code := randomID()
		p.mu.Lock()
		p.codes[code] = q.Get("nonce")
		p.mu.Unlock()
		http.Redirect(w, r, q.Get("redirect_uri")+"?code="+code+"&state="+url.QueryEscape(q.Get("state")), http.StatusFound)
	})
	mux.HandleFunc("/token", func(w http.ResponseWriter, r *http.Request) {
		r.ParseForm()
		p.mu.Lock()
		nonce, ok := p.codes[r.Form.Get("code")]
		delete(p.codes, r.Form.Get("code"))
		p.mu.Unlock()
		if !ok || r.Form.Get("code_verifier") == "" {
			http.Error(w, `{"error":"invalid_grant"}`, 400)
			return
		}
		sig, _ := jose.NewSigner(jose.SigningKey{Algorithm: jose.RS256, Key: key}, (&jose.SignerOptions{}).WithType("JWT").WithHeader("kid", "k1"))
		c := map[string]any{"iss": iss(), "aud": "grepalert", "sub": "u1", "nonce": nonce,
			"iat": time.Now().Unix(), "exp": time.Now().Add(5 * time.Minute).Unix()}
		for k, v := range p.claims {
			c[k] = v
		}
		tok, _ := jwt.Signed(sig).Claims(c).Serialize()
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]any{"access_token": "x", "token_type": "Bearer", "id_token": tok, "expires_in": 300})
	})
	return p
}

// ssoLogin follows the full redirect dance like a browser would.
func (h *authHarness) ssoLogin(c *http.Client) (*http.Response, error) {
	next := h.ts.URL + "/auth/oidc?next=/%23group/web"
	for i := 0; i < 5; i++ {
		resp, err := c.Get(next)
		if err != nil {
			return nil, err
		}
		resp.Body.Close()
		loc := resp.Header.Get("Location")
		if resp.StatusCode != http.StatusFound || loc == "" {
			return resp, nil
		}
		if strings.HasPrefix(loc, "/") {
			if !strings.HasPrefix(loc, "/auth/") {
				return resp, nil // back on the dashboard
			}
			loc = h.ts.URL + loc
		}
		next = loc
	}
	return nil, fmt.Errorf("too many redirects")
}

func TestOIDCLogin(t *testing.T) {
	idp := newFakeIdP(t, map[string]any{"email": "Dana@Example.com", "email_verified": true, "name": "Dana",
		"groups": []string{"/db-team", "everyone"}})
	var h *authHarness
	h = newAuthHarness(t, func(c *Config) {
		c.Auth.OIDC = &OIDCConfig{Name: "Example SSO", Issuer: idp.srv.URL, ClientID: "grepalert", ClientSecret: "s",
			RedirectURL: "PLACEHOLDER", AllowedDomains: []string{"example.com"}}
	})
	h.srv.cfg.Auth.OIDC.RedirectURL = h.ts.URL + "/auth/callback"
	root := h.login("root", "correct horse battery staple")
	h.req(root, "POST", "/api/groups", map[string]any{"name": "db", "source": "manual", "rules": groupRules})
	h.req(root, "POST", "/api/access", map[string]string{"subject": "group:db-team", "role": "editor", "group": "db"})

	c := h.client()
	resp, err := h.ssoLogin(c)
	if err != nil {
		t.Fatal(err)
	}
	if loc := resp.Header.Get("Location"); resp.StatusCode != http.StatusFound || loc != "/#group/web" {
		t.Fatalf("after SSO: %d %q", resp.StatusCode, loc)
	}
	_, body := h.req(c, "GET", "/api/me", nil)
	if !strings.Contains(body, `"user":"dana@example.com"`) || !strings.Contains(body, `"db-team"`) || !strings.Contains(body, `"db":"editor"`) {
		t.Fatalf("me after SSO: %s", body)
	}
	if code, _ := h.req(c, "PUT", "/api/groups/db/rules", strings.Replace(groupRules, "ERROR", "PANIC", 1)); code != 200 {
		t.Fatalf("group-claim editor could not edit: %d", code)
	}

	// Signing out of an SSO session also ends the provider session.
	code, body := h.req(c, "POST", "/auth/logout", nil)
	var out struct{ Redirect string }
	json.Unmarshal([]byte(body), &out)
	lu, _ := url.Parse(out.Redirect)
	if code != 200 || !strings.HasPrefix(out.Redirect, idp.srv.URL+"/logout?") || lu.Query().Get("id_token_hint") == "" ||
		lu.Query().Get("post_logout_redirect_uri") != h.ts.URL+"/login" {
		t.Fatalf("logout: %d %s", code, body)
	}
	if code, _ := h.req(c, "GET", "/api/me", nil); code != http.StatusUnauthorized {
		t.Fatal("still signed in after logout")
	}

	// Wrong domain is refused.
	idp.claims["email"] = "eve@evil.test"
	resp, _ = h.ssoLogin(h.client())
	if loc := resp.Header.Get("Location"); !strings.HasPrefix(loc, "/login?error=") {
		t.Fatalf("foreign domain admitted: %q", loc)
	}

	// A callback with a forged state is refused.
	c2 := h.client()
	r2, _ := c2.Get(h.ts.URL + "/auth/oidc")
	r2.Body.Close()
	r3, _ := c2.Get(h.ts.URL + "/auth/callback?code=x&state=forged")
	if loc := r3.Header.Get("Location"); !strings.HasPrefix(loc, "/login?error=") {
		t.Fatalf("forged state accepted: %q", loc)
	}

	var ok, failed int
	for _, e := range h.srv.audit.Entries(100, "login.oidc") {
		if e.Result == "ok" {
			ok++
		} else {
			failed++
		}
	}
	if ok != 1 || failed < 2 {
		t.Fatalf("audit oidc logins ok=%d failed=%d", ok, failed)
	}
}

func TestSafeNext(t *testing.T) {
	for in, want := range map[string]string{"/#x": "/#x", "//evil.com": "/", "https://evil.com": "/", "/\\evil": "/", "": "/"} {
		if got := safeNext(in); got != want {
			t.Errorf("safeNext(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestLineDiff(t *testing.T) {
	d := lineDiff("a\nb\nc\nd\ne\nf\ng", "a\nb\nc\nX\ne\nf\ng")
	if !strings.Contains(d, "- d\n+ X\n") || strings.Contains(d, "  a\n") {
		t.Fatalf("diff:\n%s", d)
	}
	if lineDiff("same", "same") != "" {
		t.Fatal("equal texts should give an empty diff")
	}
}

// The provider is reached at an internal URL but issues tokens under its
// public URL (Docker/Kubernetes); discovery_url makes that work.
func TestOIDCDiscoveryURL(t *testing.T) {
	idp := newFakeIdP(t, map[string]any{"email": "kim@example.com", "email_verified": true})
	idp.issuer = "http://sso.public.example/realms/acme"
	for _, withDiscovery := range []bool{false, true} {
		h := newAuthHarness(t, func(c *Config) {
			c.Auth.OIDC = &OIDCConfig{Issuer: idp.issuer, ClientID: "grepalert", RedirectURL: "PLACEHOLDER"}
			if withDiscovery {
				c.Auth.OIDC.DiscoveryURL = idp.srv.URL
			}
		})
		h.srv.cfg.Auth.OIDC.RedirectURL = h.ts.URL + "/auth/callback"
		resp, err := h.ssoLogin(h.client())
		if err != nil {
			t.Fatal(err)
		}
		loc := resp.Header.Get("Location")
		if withDiscovery && loc != "/#group/web" {
			t.Fatalf("with discovery_url: redirected to %q", loc)
		}
		if !withDiscovery && !strings.HasPrefix(loc, "/login?error=") {
			t.Fatalf("without discovery_url the public issuer is unreachable, want an error, got %q", loc)
		}
	}
}
