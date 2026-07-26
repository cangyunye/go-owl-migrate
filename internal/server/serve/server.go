package serve

import (
	"encoding/json"
	"net/http"
	"sort"
	"strconv"
	"sync"

	"gopkg.in/yaml.v3"

	"github.com/cangyunye/go-owl-migrate/internal/config"
	md "github.com/cangyunye/go-owl-migrate/internal/metadata"
	"github.com/cangyunye/go-owl-migrate/internal/service"
)

type Config struct {
	Store     *service.JobStore
	MasterURL string
}

type Server struct {
	store     *service.JobStore
	masterURL string
	hub       *Hub

	mu          sync.RWMutex
	cfg         *config.Config
	schemaModel *md.SchemaModel
}

func NewServer(cfg Config) *Server {
	return &Server{
		store:     cfg.Store,
		masterURL: cfg.MasterURL,
		hub:       NewHub(cfg.Store),
		cfg:       &config.Config{},
	}
}

func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()

	mux.HandleFunc("GET /api/v1/health", s.handleHealth)
	mux.HandleFunc("GET /api/v1/jobs", s.handleListJobs)
	mux.HandleFunc("GET /api/v1/jobs/{id}", s.handleGetJob)
	mux.HandleFunc("GET /api/v1/jobs/{id}/events", s.handleGetJobEvents)
	mux.HandleFunc("GET /api/v1/jobs/{id}/checkpoints", s.handleGetJobCheckpoints)
	mux.HandleFunc("GET /api/v1/dialects", s.handleGetDialects)
	mux.HandleFunc("GET /api/v1/config", s.handleGetConfig)
	mux.HandleFunc("PUT /api/v1/config", s.handlePutConfig)
	mux.HandleFunc("POST /api/v1/metadata/load", s.handleMetadataLoad)
	mux.HandleFunc("GET /api/v1/metadata/tables", s.handleMetadataTables)
	mux.HandleFunc("GET /api/v1/jobs/{id}/ws", s.handleWebSocket)
	mux.HandleFunc("POST /api/v1/migrate", s.handleStartMigrate)
	mux.HandleFunc("POST /api/v1/export", s.handleStartExport)
	mux.HandleFunc("POST /api/v1/import", s.handleStartImport)
	mux.HandleFunc("DELETE /api/v1/jobs/{id}", s.handleCancelJob)

	s.registerPages(mux)
	s.registerDocs(mux)

	return withCORS(mux)
}

func (s *Server) handleHealth(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

func (s *Server) handleListJobs(w http.ResponseWriter, r *http.Request) {
	jobs, err := s.store.ListJobs(100)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	if jobs == nil {
		jobs = []service.Job{}
	}
	writeJSON(w, http.StatusOK, jobs)
}

func (s *Server) handleGetJob(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	job, err := s.store.GetJob(id)
	if err != nil {
		writeError(w, http.StatusNotFound, "job not found")
		return
	}
	writeJSON(w, http.StatusOK, job)
}

func (s *Server) handleGetJobEvents(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	afterSeq := int64(0)
	if v := r.URL.Query().Get("after_seq"); v != "" {
		afterSeq, _ = strconv.ParseInt(v, 10, 64)
	}

	events, err := s.store.GetEvents(id, afterSeq)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	if events == nil {
		events = []service.ProgressEvent{}
	}
	writeJSON(w, http.StatusOK, events)
}

func (s *Server) handleGetJobCheckpoints(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	cps, err := s.store.GetCheckpoints(id)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	if cps == nil {
		cps = []service.JobCheckpoint{}
	}
	writeJSON(w, http.StatusOK, cps)
}

func (s *Server) handleGetDialects(w http.ResponseWriter, r *http.Request) {
	names := make([]string, 0, len(config.ValidDialects))
	for name := range config.ValidDialects {
		names = append(names, name)
	}
	sort.Strings(names)
	writeJSON(w, http.StatusOK, names)
}

func (s *Server) handleGetConfig(w http.ResponseWriter, r *http.Request) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	m, err := configToMap(s.cfg)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, m)
}

func (s *Server) handlePutConfig(w http.ResponseWriter, r *http.Request) {
	var raw map[string]any
	if err := json.NewDecoder(r.Body).Decode(&raw); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON: "+err.Error())
		return
	}

	cfg, err := mapToConfig(raw)
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid config: "+err.Error())
		return
	}

	s.mu.Lock()
	s.cfg = cfg
	s.mu.Unlock()

	writeJSON(w, http.StatusOK, map[string]string{"status": "saved"})
}

func (s *Server) handleMetadataLoad(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Metadata config.MetadataConfig `json:"metadata"`
		Source   config.DBConfig       `json:"source"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON: "+err.Error())
		return
	}

	cfg := &config.Config{
		Metadata: req.Metadata,
		Source:   req.Source,
	}

	sm, err := service.LoadMetadata(cfg)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}

	s.mu.Lock()
	s.schemaModel = sm
	s.cfg.Metadata = req.Metadata
	s.cfg.Source = req.Source
	s.mu.Unlock()

	tables := sm.GetTables()
	tableSummaries := make([]map[string]any, 0, len(tables))
	for _, tbl := range tables {
		tableSummaries = append(tableSummaries, map[string]any{
			"schema":      tbl.TableSchema,
			"name":        tbl.TableName,
			"columns":     len(tbl.Columns),
			"primary_key": pkNames(tbl),
			"row_count":   tbl.RowCount,
		})
	}

	writeJSON(w, http.StatusOK, map[string]any{
		"table_count": len(tables),
		"tables":      tableSummaries,
	})
}

func (s *Server) handleMetadataTables(w http.ResponseWriter, r *http.Request) {
	s.mu.RLock()
	sm := s.schemaModel
	s.mu.RUnlock()

	if sm == nil {
		writeError(w, http.StatusBadRequest, "metadata not loaded; call POST /api/v1/metadata/load first")
		return
	}

	tables := sm.GetTables()
	result := make([]map[string]any, 0, len(tables))
	for _, tbl := range tables {
		cols := make([]map[string]any, 0, len(tbl.Columns))
		for _, col := range tbl.Columns {
			cols = append(cols, map[string]any{
				"name":     col.ColumnName,
				"type":     col.DataType,
				"length":   col.DataLength,
				"nullable": col.Nullable,
				"default":  col.DefaultValue,
			})
		}
		result = append(result, map[string]any{
			"schema":      tbl.TableSchema,
			"name":        tbl.TableName,
			"columns":     cols,
			"primary_key": pkNames(tbl),
			"row_count":   tbl.RowCount,
			"comment":     tbl.TableComment,
		})
	}
	writeJSON(w, http.StatusOK, result)
}

func pkNames(tbl *md.TableDef) []string {
	pks := tbl.GetPrimaryKeys()
	names := make([]string, len(pks))
	for i, pk := range pks {
		names[i] = pk.ColumnName
	}
	return names
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(v)
}

func writeError(w http.ResponseWriter, status int, msg string) {
	writeJSON(w, status, map[string]string{"error": msg})
}

func withCORS(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Access-Control-Allow-Origin", "*")
		w.Header().Set("Access-Control-Allow-Methods", "GET, POST, PUT, DELETE, OPTIONS")
		w.Header().Set("Access-Control-Allow-Headers", "Content-Type")
		if r.Method == "OPTIONS" {
			w.WriteHeader(http.StatusNoContent)
			return
		}
		next.ServeHTTP(w, r)
	})
}

func configToMap(cfg *config.Config) (map[string]any, error) {
	data, err := yaml.Marshal(cfg)
	if err != nil {
		return nil, err
	}
	var m map[string]any
	if err := yaml.Unmarshal(data, &m); err != nil {
		return nil, err
	}
	return m, nil
}

func mapToConfig(m map[string]any) (*config.Config, error) {
	data, err := yaml.Marshal(m)
	if err != nil {
		return nil, err
	}
	var cfg config.Config
	if err := yaml.Unmarshal(data, &cfg); err != nil {
		return nil, err
	}
	return &cfg, nil
}
