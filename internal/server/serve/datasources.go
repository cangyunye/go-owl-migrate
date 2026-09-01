package serve

import (
	"net/http"

	"github.com/cangyunye/go-owl-migrate/internal/datasource"
	"github.com/cangyunye/go-owl-migrate/internal/dsnfields"
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

// handleGetDataSource returns one profile plus its structured DSN fields for
// the edit form. The stored password is never returned; password_set tells the
// form to show a "leave blank to keep" placeholder.
func (s *Server) handleGetDataSource(w http.ResponseWriter, r *http.Request) {
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
	dsn, err := resolveDataSourceDSN(store, rec)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	fields, _ := dsnfields.Decompose(rec.Type, dsn)
	passwordSet := fields.Password != ""
	fields.Password = ""
	writeJSON(w, http.StatusOK, map[string]any{
		"name":         rec.Name,
		"type":         rec.Type,
		"schema":       rec.Schema,
		"remark":       rec.Remark,
		"updated":      rec.Updated,
		"password_set": passwordSet,
		"fields":       fields,
	})
}

// resolveDataSourceDSN decrypts a record's DSN, tolerating a nil vault.
func resolveDataSourceDSN(store *datasource.Store, rec *datasource.Record) (string, error) {
	_, _, dsn, err := store.Resolve(rec.Name)
	if err != nil {
		return "", err
	}
	return dsn, nil
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
// the body keeps the previously-stored ciphertext intact. Structured fields
// (req.Fields) are also accepted: Build keeps the stored password when its
// Password value is blank.
func (s *Server) handleUpdateDataSource(w http.ResponseWriter, r *http.Request) {
	var req dataSourceReq
	if !decodeJSON(w, r, &req, maxBodyBytes) {
		return
	}
	s.saveDataSource(w, req, r.PathValue("name"))
}

// dataSourceReq is the create/update request body. Either DSN (raw string) or
// Fields (structured) may be supplied; the latter is built into a DSN.
type dataSourceReq struct {
	Name   string            `json:"name"`
	Type   string            `json:"type"`
	Schema string            `json:"schema"`
	DSN    string            `json:"dsn"`
	Remark string            `json:"remark"`
	Fields *dsnfields.Fields `json:"fields"`
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

	dsn := req.DSN
	if req.Fields != nil {
		oldDSN := ""
		if urlName != "" {
			if rec, err := store.Get(urlName); err == nil {
				if d, err := resolveDataSourceDSN(store, rec); err == nil {
					oldDSN = d
				}
			}
		}
		built, err := dsnfields.Build(req.Type, *req.Fields, oldDSN)
		if err != nil {
			writeError(w, http.StatusBadRequest, "dsn: "+err.Error())
			return
		}
		dsn = built
	}

	if err := store.Put(name, req.Type, req.Schema, dsn, req.Remark); err != nil {
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
