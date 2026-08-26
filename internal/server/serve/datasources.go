package serve

import (
	"net/http"

	"github.com/cangyunye/go-owl-migrate/internal/datasource"
)

// handleListDataSources returns the DSN-free projections of every saved data
// source, so the picker and the 数据源 page can list them without ever
// exposing stored passwords.
func (s *Server) handleListDataSources(w http.ResponseWriter, r *http.Request) {
	store, err := s.dsStore()
	if err != nil {
		writeError(w, http.StatusInternalServerError, "data-source store: "+err.Error())
		return
	}
	list, err := store.List()
	if err != nil {
		writeError(w, http.StatusInternalServerError, "list data sources: "+err.Error())
		return
	}
	if list == nil {
		list = []datasource.Info{}
	}
	writeJSON(w, http.StatusOK, list)
}

// handleCreateDataSource saves a new profile (or replaces the same name).
func (s *Server) handleCreateDataSource(w http.ResponseWriter, r *http.Request) {
	var req dataSourceReq
	if !decodeJSON(w, r, &req, maxBodyBytes) {
		return
	}
	s.saveDataSource(w, req, "")
}

// handleUpdateDataSource updates an existing profile by name. An empty dsn in
// the body keeps the previously-stored ciphertext intact.
func (s *Server) handleUpdateDataSource(w http.ResponseWriter, r *http.Request) {
	var req dataSourceReq
	if !decodeJSON(w, r, &req, maxBodyBytes) {
		return
	}
	s.saveDataSource(w, req, r.PathValue("name"))
}

// dataSourceReq is the create/update request body.
type dataSourceReq struct {
	Name   string `json:"name"`
	Type   string `json:"type"`
	Schema string `json:"schema"`
	DSN    string `json:"dsn"`
	Remark string `json:"remark"`
}

func (s *Server) saveDataSource(w http.ResponseWriter, req dataSourceReq, urlName string) {
	store, err := s.dsStore()
	if err != nil {
		writeError(w, http.StatusInternalServerError, "data-source store: "+err.Error())
		return
	}
	name := req.Name
	if urlName != "" {
		name = urlName
	}
	if err := store.Put(name, req.Type, req.Schema, req.DSN, req.Remark); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	info, err := store.Get(name)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "read data source: "+err.Error())
		return
	}
	writeJSON(w, http.StatusOK, datasource.Info{
		Name:    info.Name,
		Type:    info.Type,
		Schema:  info.Schema,
		Remark:  info.Remark,
		Updated: info.Updated,
	})
}

// handleDeleteDataSource removes a profile.
func (s *Server) handleDeleteDataSource(w http.ResponseWriter, r *http.Request) {
	store, err := s.dsStore()
	if err != nil {
		writeError(w, http.StatusInternalServerError, "data-source store: "+err.Error())
		return
	}
	if err := store.Delete(r.PathValue("name")); err != nil {
		writeError(w, http.StatusInternalServerError, "delete data source: "+err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "deleted"})
}

// handlePickDataSource returns the type + schema of a named data source so the
// config form can fill them. The DSN itself is never returned to the browser;
// the form instead stores a datasource:<name> reference that is resolved
// server-side when the config is built.
func (s *Server) handlePickDataSource(w http.ResponseWriter, r *http.Request) {
	store, err := s.dsStore()
	if err != nil {
		writeError(w, http.StatusInternalServerError, "data-source store: "+err.Error())
		return
	}
	name := r.PathValue("name")
	rec, err := store.Get(name)
	if err != nil {
		writeError(w, http.StatusNotFound, "data source not found")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"name":   rec.Name,
		"type":   rec.Type,
		"schema": rec.Schema,
		"remark": rec.Remark,
		"ref":    datasource.Ref(rec.Name),
	})
}
