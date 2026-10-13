package agent

import (
	"crypto/subtle"
	"encoding/json"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/fadhlanhawali/grepalert/internal/rules"
)

const maxRulesBody = 1 << 20

// Handler returns the agent's HTTP API, used by the dashboard.
//
//	GET  /healthz                 liveness (no auth)
//	GET  /api/v1/status           runtime status, files, rule stats, memory
//	GET  /api/v1/rules            current rules YAML (X-Rules-Hash header)
//	PUT  /api/v1/rules            replace rules (validated, persisted, hot-applied)
//	POST /api/v1/rules/validate   validate rules without applying
//	GET  /api/v1/events?limit=N   recent events, newest first
//	POST /api/v1/test             {"line": "..."} → rules that would match
func (a *Agent) Handler() http.Handler { return a.auth(a.api()) }

// api is the unauthenticated API mux. The inbound listener wraps it with
// token auth; the dashboard tunnel calls it directly, because the tunnel
// connection is itself authenticated.
func (a *Agent) api() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte("ok"))
	})
	mux.HandleFunc("GET /api/v1/status", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, http.StatusOK, a.Status())
	})
	mux.HandleFunc("GET /api/v1/rules", func(w http.ResponseWriter, r *http.Request) {
		c := a.Rules()
		w.Header().Set("Content-Type", "application/yaml")
		w.Header().Set("X-Rules-Hash", c.Hash)
		w.Write(c.Raw)
	})
	mux.HandleFunc("PUT /api/v1/rules", func(w http.ResponseWriter, r *http.Request) {
		raw, err := io.ReadAll(http.MaxBytesReader(w, r.Body, maxRulesBody))
		if err != nil {
			writeErr(w, http.StatusRequestEntityTooLarge, err)
			return
		}
		origin := r.Header.Get("X-Rules-Origin")
		if origin == "" {
			origin = "api"
		}
		c, changed, err := a.ApplyRules(raw, origin)
		if err != nil {
			writeErr(w, http.StatusUnprocessableEntity, err)
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"hash": c.Hash, "rules": len(c.Rules), "unchanged": !changed})
	})
	mux.HandleFunc("POST /api/v1/rules/validate", func(w http.ResponseWriter, r *http.Request) {
		raw, err := io.ReadAll(http.MaxBytesReader(w, r.Body, maxRulesBody))
		if err != nil {
			writeErr(w, http.StatusRequestEntityTooLarge, err)
			return
		}
		c, err := rules.Parse(raw)
		if err != nil {
			writeErr(w, http.StatusUnprocessableEntity, err)
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"hash": c.Hash, "rules": len(c.Rules)})
	})
	mux.HandleFunc("GET /api/v1/events", func(w http.ResponseWriter, r *http.Request) {
		limit, _ := strconv.Atoi(r.URL.Query().Get("limit"))
		if limit <= 0 {
			limit = 100
		}
		writeJSON(w, http.StatusOK, a.Events(limit))
	})
	mux.HandleFunc("POST /api/v1/test", func(w http.ResponseWriter, r *http.Request) {
		var req struct {
			Line string `json:"line"`
		}
		if err := json.NewDecoder(io.LimitReader(r.Body, 64*1024)).Decode(&req); err != nil {
			writeErr(w, http.StatusBadRequest, err)
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"matched": a.TestLine(req.Line)})
	})
	return mux
}

func (a *Agent) auth(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if a.cfg.Token != "" && r.URL.Path != "/healthz" {
			got := strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer ")
			if subtle.ConstantTimeCompare([]byte(got), []byte(a.cfg.Token)) != 1 {
				writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "unauthorized"})
				return
			}
		}
		next.ServeHTTP(w, r)
	})
}

// Server returns an http.Server for the agent API with conservative timeouts.
func (a *Agent) Server() *http.Server {
	return &http.Server{
		Addr:              a.cfg.Listen,
		Handler:           a.Handler(),
		ReadHeaderTimeout: 5 * time.Second,
		ReadTimeout:       30 * time.Second,
		WriteTimeout:      30 * time.Second,
		IdleTimeout:       60 * time.Second,
		MaxHeaderBytes:    16 * 1024,
	}
}

func (a *Agent) Config() Config { return a.cfg }

func writeJSON(w http.ResponseWriter, code int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	json.NewEncoder(w).Encode(v)
}

func writeErr(w http.ResponseWriter, code int, err error) {
	writeJSON(w, code, map[string]string{"error": err.Error()})
}
