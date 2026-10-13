package dashboard

import (
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
)

// Admin APIs for local users, role bindings and the audit log.

func (s *Server) handleListUsers(w http.ResponseWriter, r *http.Request) {
	out := map[string]any{"users": s.store.Users()}
	if s.cfg.Auth.Username != "" {
		out["builtin_admin"] = s.cfg.Auth.Username // from the config file
	}
	writeJSON(w, http.StatusOK, out)
}

// handleSetUser creates a local user or resets its password.
func (s *Server) handleSetUser(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("name")
	var req struct {
		Password string `json:"password"`
	}
	if !readJSON(w, r, &req) {
		return
	}
	if strings.EqualFold(name, s.cfg.Auth.Username) {
		writeErr(w, http.StatusBadRequest, fmt.Errorf("%q is the built-in admin from the config file; change its password there", name))
		return
	}
	created, err := s.store.SetUser(name, req.Password)
	if err != nil {
		writeErr(w, http.StatusBadRequest, err)
		return
	}
	action := "user.password_reset"
	if created {
		action = "user.create"
	}
	s.audit.record(r, action, name, "", "ok", "", "")
	writeJSON(w, http.StatusOK, map[string]any{"username": name, "created": created})
}

func (s *Server) handleDeleteUser(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("name")
	if strings.EqualFold(name, authOf(r).id.User) {
		writeErr(w, http.StatusBadRequest, fmt.Errorf("you cannot delete your own account"))
		return
	}
	if err := s.store.DeleteUser(name); err != nil {
		writeErr(w, http.StatusNotFound, err)
		return
	}
	s.audit.record(r, "user.delete", name, "", "ok", "sessions ended", "")
	writeJSON(w, http.StatusOK, map[string]string{"deleted": name})
}

func (s *Server) handleListBindings(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, map[string]any{"bindings": orEmpty(s.store.Bindings()), "config_admins": orEmpty(s.bootstrapAdmins())})
}

func (s *Server) handleAddBinding(w http.ResponseWriter, r *http.Request) {
	var req RoleBinding
	if !readJSON(w, r, &req) {
		return
	}
	req.Subject = strings.TrimSpace(req.Subject)
	req.CreatedBy = authOf(r).id.User
	b, err := s.store.AddBinding(req)
	if err != nil {
		writeErr(w, http.StatusBadRequest, err)
		return
	}
	s.audit.record(r, "access.grant", b.Subject, b.Group, "ok", string(b.Role)+scopeDesc(b.Group), "")
	writeJSON(w, http.StatusOK, b)
}

func (s *Server) handleDeleteBinding(w http.ResponseWriter, r *http.Request) {
	b, err := s.store.DeleteBinding(r.PathValue("id"))
	if err != nil {
		writeErr(w, http.StatusNotFound, err)
		return
	}
	s.audit.record(r, "access.revoke", b.Subject, b.Group, "ok", string(b.Role)+scopeDesc(b.Group), "")
	writeJSON(w, http.StatusOK, b)
}

func (s *Server) handleAudit(w http.ResponseWriter, r *http.Request) {
	limit := 500
	fmt.Sscanf(r.URL.Query().Get("limit"), "%d", &limit)
	writeJSON(w, http.StatusOK, s.audit.Entries(min(max(limit, 1), auditMemory), r.URL.Query().Get("q")))
}

// handleAuditExport streams the current audit file (JSON lines), for SIEMs.
func (s *Server) handleAuditExport(w http.ResponseWriter, r *http.Request) {
	f, err := os.Open(s.cfg.AuditFile)
	if err != nil {
		writeErr(w, http.StatusNotFound, err)
		return
	}
	defer f.Close()
	s.audit.record(r, "audit.export", s.cfg.AuditFile, "", "ok", "", "")
	w.Header().Set("Content-Type", "application/x-ndjson")
	w.Header().Set("Content-Disposition", `attachment; filename="grepalert-audit.jsonl"`)
	io.Copy(w, f)
}
