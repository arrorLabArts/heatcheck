package httpapi

import (
	"net/http"
	"strings"

	apidocs "github.com/arrorLabArts/heatcheck/api"
	"github.com/go-chi/chi/v5"
)

func (a *API) openAPISpec(w http.ResponseWriter, _ *http.Request) {
	w.Header().Set("Content-Type", "application/yaml")
	w.Header().Set("Cache-Control", "public, max-age=300")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(apidocs.Spec)
}

func (a *API) health(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

func (a *API) ready(w http.ResponseWriter, r *http.Request) {
	if err := a.store.Ping(r.Context()); err != nil {
		a.logger.Error("readiness check failed", "error", err)
		writeError(w, http.StatusServiceUnavailable, "not_ready", "The service is not ready.", nil)
		return
	}
	if a.requireWorker {
		healthy, err := a.store.HasHealthyWorker(r.Context(), a.workerStaleAfter)
		if err != nil || !healthy {
			a.logger.Error("worker readiness check failed", "error", err, "healthy", healthy)
			writeError(w, http.StatusServiceUnavailable, "worker_not_ready", "The background worker is not ready.", nil)
			return
		}
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "ready"})
}

func (a *API) listPolicies(w http.ResponseWriter, r *http.Request) {
	policies, err := a.store.ListCurrentPolicies(r.Context())
	if err != nil {
		a.logger.Error("list policies", "error", err)
		handleStoreError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"data": policies})
}

func (a *API) getPolicy(w http.ResponseWriter, r *http.Request) {
	kind := strings.TrimSpace(chi.URLParam(r, "kind"))
	policy, err := a.store.GetCurrentPolicy(r.Context(), kind)
	if err != nil {
		handleStoreError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"data": policy})
}
