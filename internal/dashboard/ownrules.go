package dashboard

import (
	"errors"
	"fmt"
	"io"
	"net/http"
)

// handleRestoreOwnRules makes an agent run the rules it had before a group
// replaced them (the agent keeps a copy, rules.before-group.yaml). Only for
// agents outside a group: a group would push its own rules right back.
func (s *Server) handleRestoreOwnRules(w http.ResponseWriter, r *http.Request) {
	rec, ok := s.store.Agent(r.PathValue("name"))
	if !ok {
		writeErr(w, http.StatusNotFound, errors.New("unknown agent"))
		return
	}
	if g := EffectiveGroup(rec, s.store.Groups()); g != "" {
		writeErr(w, http.StatusConflict, fmt.Errorf("this agent is in group %q, which would push its rules right back; move it out of the group first", g))
		return
	}
	c := s.client(rec)
	read := func(path string) string {
		resp, err := c.call(r.Context(), http.MethodGet, path, nil, nil)
		if err != nil {
			return ""
		}
		defer resp.Body.Close()
		if resp.StatusCode != http.StatusOK {
			return ""
		}
		b, _ := io.ReadAll(io.LimitReader(resp.Body, maxProxyBody))
		return string(b)
	}
	before, own := read("/api/v1/rules"), read("/api/v1/rules/own")
	resp, err := c.call(r.Context(), http.MethodPost, "/api/v1/rules/own/restore", nil, nil)
	if err != nil {
		writeErr(w, http.StatusBadGateway, err)
		return
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(io.LimitReader(resp.Body, maxProxyBody))
	if resp.StatusCode == http.StatusOK {
		s.audit.record(r, "agent.rules.restore", rec.Name, "", "ok", "restored the agent's own rules from before its group", lineDiff(before, own))
	} else {
		s.audit.record(r, "agent.rules.restore", rec.Name, "", "failed", fmt.Sprintf("agent answered HTTP %d", resp.StatusCode), "")
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(resp.StatusCode)
	w.Write(body)
}
