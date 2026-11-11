package api

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"path/filepath"
	"strings"

	"trevorbuild/internal/appicon"
	"trevorbuild/internal/builds"
	"trevorbuild/internal/config"
	"trevorbuild/internal/storage"
)

type handler struct {
	builds *builds.Manager
}

func NewHandler(manager *builds.Manager) http.Handler {
	instance := &handler{builds: manager}
	mux := http.NewServeMux()
	mux.HandleFunc("GET /health", instance.health)
	mux.HandleFunc("GET /api/v1/frameworks", instance.frameworks)
	mux.HandleFunc("POST /api/v1/builds", instance.createBuild)
	mux.HandleFunc("GET /api/v1/builds/{id}", instance.getBuild)
	mux.HandleFunc("DELETE /api/v1/builds/{id}", instance.cancelBuild)
	mux.HandleFunc("GET /api/v1/builds/{id}/download", instance.download)
	return securityHeaders(mux)
}

func (h *handler) health(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "service": "trevorbuild"})
}

func (h *handler) frameworks(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, map[string]any{"frameworks": config.SupportedFrameworks()})
}

func (h *handler) createBuild(w http.ResponseWriter, r *http.Request) {
	r.Body = http.MaxBytesReader(w, r.Body, storage.MaxUploadBytes+storage.MaxIconUploadBytes+(2<<20))
	if err := r.ParseMultipartForm(32 << 20); err != nil {
		if r.MultipartForm != nil {
			defer r.MultipartForm.RemoveAll()
		}
		var maxBytesError *http.MaxBytesError
		if errors.As(err, &maxBytesError) {
			http.Error(w, "request exceeds the ZIP and icon upload limits", http.StatusRequestEntityTooLarge)
		} else {
			http.Error(w, "invalid multipart request or ZIP exceeds 100 MiB", http.StatusBadRequest)
		}
		return
	}
	if r.MultipartForm != nil {
		defer r.MultipartForm.RemoveAll()
	}
	cfg, err := config.Parse(r.FormValue("config"))
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	file, header, err := r.FormFile("file")
	if err != nil {
		http.Error(w, "multipart field 'file' is required", http.StatusBadRequest)
		return
	}
	defer file.Close()
	if !strings.EqualFold(filepath.Ext(header.Filename), ".zip") {
		http.Error(w, "upload must be a .zip file", http.StatusBadRequest)
		return
	}
	var iconData []byte
	if iconHeaders := r.MultipartForm.File["icon"]; len(iconHeaders) > 1 {
		http.Error(w, "multipart field 'icon' may only be uploaded once", http.StatusBadRequest)
		return
	} else if len(iconHeaders) == 1 {
		iconFile, _, err := r.FormFile("icon")
		if err != nil {
			http.Error(w, "could not read multipart field 'icon'", http.StatusBadRequest)
			return
		}
		defer iconFile.Close()
		iconData, err = io.ReadAll(io.LimitReader(iconFile, appicon.MaxUploadBytes+1))
		if err != nil {
			http.Error(w, fmt.Sprintf("read icon upload: %v", err), http.StatusBadRequest)
			return
		}
		if err := appicon.Validate(iconData); err != nil {
			status := http.StatusBadRequest
			if int64(len(iconData)) > appicon.MaxUploadBytes {
				status = http.StatusRequestEntityTooLarge
			}
			http.Error(w, err.Error(), status)
			return
		}
	}
	var job builds.Job
	if len(iconData) > 0 {
		job, err = h.builds.Start(cfg, file, bytes.NewReader(iconData))
	} else {
		job, err = h.builds.Start(cfg, file)
	}
	if err != nil {
		if strings.Contains(err.Error(), "100 MiB") {
			http.Error(w, err.Error(), http.StatusRequestEntityTooLarge)
		} else {
			http.Error(w, err.Error(), http.StatusInternalServerError)
		}
		return
	}
	writeJSON(w, http.StatusAccepted, map[string]any{
		"job":       job,
		"statusUrl": h.builds.StatusURL(job.ID),
	})
}

func (h *handler) getBuild(w http.ResponseWriter, r *http.Request) {
	job, err := h.builds.Get(r.PathValue("id"))
	if err != nil {
		http.NotFound(w, r)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"job": job})
}

func (h *handler) cancelBuild(w http.ResponseWriter, r *http.Request) {
	job, err := h.builds.Cancel(r.PathValue("id"))
	if errors.Is(err, builds.ErrNotFound) {
		http.NotFound(w, r)
		return
	}
	if errors.Is(err, builds.ErrAlreadyFinished) {
		http.Error(w, err.Error(), http.StatusConflict)
		return
	}
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"job": job})
}

func (h *handler) download(w http.ResponseWriter, r *http.Request) {
	path, filename, err := h.builds.Artifact(r.PathValue("id"))
	if errors.Is(err, builds.ErrNotFound) {
		http.NotFound(w, r)
		return
	}
	if errors.Is(err, builds.ErrArtifactNotReady) {
		http.Error(w, err.Error(), http.StatusConflict)
		return
	}
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Disposition", `attachment; filename="`+filename+`"`)
	http.ServeFile(w, r, path)
}

func securityHeaders(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-Content-Type-Options", "nosniff")
		w.Header().Set("X-Frame-Options", "DENY")
		w.Header().Set("Referrer-Policy", "no-referrer")
		w.Header().Set("Access-Control-Allow-Origin", "*")
		w.Header().Set("Access-Control-Allow-Methods", "GET, POST, DELETE, OPTIONS")
		w.Header().Set("Access-Control-Allow-Headers", "Content-Type")
		if r.Method == http.MethodOptions {
			w.WriteHeader(http.StatusNoContent)
			return
		}
		next.ServeHTTP(w, r)
	})
}

func writeJSON(w http.ResponseWriter, status int, value any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(value)
}
