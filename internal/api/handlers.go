package api

import (
	"encoding/json"
	"net/http"
)

// ── helpers ───────────────────────────────────────────────────────────────────

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

func writeError(w http.ResponseWriter, status int, msg string) {
	writeJSON(w, status, map[string]string{"error": msg})
}

// ── handlers ──────────────────────────────────────────────────────────────────

func (s *Server) health(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

func (s *Server) listFiles(w http.ResponseWriter, r *http.Request) {
	files, err := s.svc.ListFiles(r.Context())
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"files": files})
}

func (s *Server) getFile(w http.ResponseWriter, r *http.Request) {
	path := r.PathValue("path")
	detail, err := s.svc.DescribeFile(r.Context(), path)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	if detail == nil {
		writeError(w, http.StatusNotFound, "file not indexed: "+path)
		return
	}
	writeJSON(w, http.StatusOK, detail)
}

// getFileSnippets returns snippet summaries only (no raw_content) for a file.
func (s *Server) getFileSnippets(w http.ResponseWriter, r *http.Request) {
	path := r.PathValue("path")
	detail, err := s.svc.DescribeFile(r.Context(), path)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	if detail == nil {
		writeError(w, http.StatusNotFound, "file not indexed: "+path)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"snippets": detail.Snippets})
}

func (s *Server) getSnippet(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	sn, err := s.svc.GetSnippet(r.Context(), id)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	if sn == nil {
		writeError(w, http.StatusNotFound, "snippet not found: "+id)
		return
	}
	writeJSON(w, http.StatusOK, sn)
}

func (s *Server) search(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Query string `json:"query"`
		Limit int    `json:"limit"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON body")
		return
	}
	if req.Query == "" {
		writeError(w, http.StatusBadRequest, "query is required")
		return
	}
	if req.Limit <= 0 {
		req.Limit = 5
	}
	results, err := s.svc.Search(r.Context(), req.Query, req.Limit)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"results": results})
}

func (s *Server) getDependencies(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	result, err := s.svc.GetDependencies(r.Context(), id)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	if result == nil {
		writeError(w, http.StatusNotFound, "snippet not found: "+id)
		return
	}
	writeJSON(w, http.StatusOK, result)
}

func (s *Server) getDependents(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	br, err := s.svc.GetBlastRadius(r.Context(), id)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	if br == nil {
		writeError(w, http.StatusNotFound, "snippet not found: "+id)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"snippet":    br.Snippet,
		"dependents": br.Dependents,
	})
}

func (s *Server) reindex(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Path string `json:"path"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON body")
		return
	}
	if req.Path == "" {
		writeError(w, http.StatusBadRequest, "path is required")
		return
	}
	if err := s.svc.ReindexFile(r.Context(), req.Path); err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "reindexed", "path": req.Path})
}
