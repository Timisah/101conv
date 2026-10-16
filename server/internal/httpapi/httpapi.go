package httpapi

import (
	"encoding/json"
	"net/http"

	"github.com/timisah/101conv/server/internal/catalog"
)

type API struct {
	version string
	catalog *catalog.Service
}

func New(version string, catalogService *catalog.Service) http.Handler {
	a := &API{version: version, catalog: catalogService}
	mux := http.NewServeMux()
	mux.HandleFunc("GET /healthz", a.health)
	mux.HandleFunc("GET /readyz", a.ready)
	mux.HandleFunc("GET /api/v1/catalogs", a.catalogs)
	mux.HandleFunc("GET /api/v1/catalogs/101conv-official", a.officialCatalog)
	return mux
}

func (a *API) health(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

func (a *API) ready(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, map[string]string{"status": "ready", "version": a.version})
}

func (a *API) catalogs(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, map[string][]catalog.Source{"sources": {a.catalog.Source()}})
}

func (a *API) officialCatalog(w http.ResponseWriter, _ *http.Request) {
	profiles, err := a.catalog.Available()
	if err != nil {
		writeJSON(w, http.StatusConflict, map[string]string{"code": "catalog_incompatible", "message": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"source": a.catalog.Source(), "profiles": profiles})
}

func writeJSON(w http.ResponseWriter, status int, value any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(value)
}
