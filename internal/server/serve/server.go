package serve

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"

	"gopkg.in/yaml.v3"

	"github.com/cangyunye/go-owl-migrate/internal/config"
	"github.com/cangyunye/go-owl-migrate/internal/datasource"
	"github.com/cangyunye/go-owl-migrate/internal/dscrypto"
	md "github.com/cangyunye/go-owl-migrate/internal/metadata"
	"github.com/cangyunye/go-owl-migrate/internal/paths"
	"github.com/cangyunye/go-owl-migrate/internal/service"
)

type Config struct {
	Store      *service.JobStore
	MasterURL  string
	ConfigPath string
	TempDir    string
	ConfigDir  string
	// DataSourcesDir is where reusable data-source profiles live
	// (~/.owl/migrate/datasources). Empty defaults to paths.DataSourcesDir.
	DataSourcesDir string
	Token          string
}

type Server struct {
	store          *service.JobStore
	masterURL      string
	configPath     string
	tempDir        string
	configDir      string
	dataSourcesDir string
	token          string

	mu          sync.RWMutex
	cfg         *config.Config
	schemaModel *md.SchemaModel

	dsOnce sync.Once
	dsErr  error
	ds     *datasource.Store
}

func NewServer(cfg Config) *Server {
	s := &Server{
		store:          cfg.Store,
		masterURL:      cfg.MasterURL,
		configPath:     cfg.ConfigPath,
		tempDir:        cfg.TempDir,
		configDir:      cfg.ConfigDir,
		dataSourcesDir: cfg.DataSourcesDir,
		token:          cfg.Token,
		cfg:            &config.Config{},
	}
	// Restore the last active config so it survives a server restart.
	if cfg.ConfigPath != "" {
		if data, err := os.ReadFile(cfg.ConfigPath); err == nil {
			var loaded config.Config
			if yaml.Unmarshal(data, &loaded) == nil {
				s.cfg = &loaded
			}
		}
	}
	return s
}

// dsStore lazily builds the encrypted data-source store. The key file is only
// created on first use so servers that never touch data sources stay hermetic.
func (s *Server) dsStore() (*datasource.Store, error) {
	s.dsOnce.Do(func() {
		dir := s.dataSourcesDir
		if dir == "" {
			dir = paths.DataSourcesDir()
		}
		vault, err := dscrypto.New(filepath.Dir(dir))
		if err != nil {
			s.dsErr = err
			return
		}
		s.ds = datasource.Open(dir, vault)
	})
	return s.ds, s.dsErr
}

func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()

	mux.HandleFunc("GET /api/v1/health", s.handleHealth)
	mux.HandleFunc("GET /api/v1/jobs", s.handleListJobs)
	mux.HandleFunc("GET /api/v1/jobs/{id}", s.handleGetJob)
	mux.HandleFunc("GET /api/v1/jobs/{id}/events", s.handleGetJobEvents)
	mux.HandleFunc("GET /api/v1/jobs/{id}/checkpoints", s.handleGetJobCheckpoints)
	mux.HandleFunc("GET /api/v1/jobs/{id}/output", s.handleJobOutput)
	mux.HandleFunc("GET /api/v1/jobs/{id}/output/download", s.handleJobOutputDownload)
	mux.HandleFunc("GET /api/v1/dialects", s.handleGetDialects)
	mux.HandleFunc("POST /api/v1/conn/test", s.handleTestConn)
	mux.HandleFunc("GET /api/v1/config", s.handleGetConfig)
	mux.HandleFunc("GET /api/v1/config/current", s.handleGetCurrentConfig)
	mux.HandleFunc("PUT /api/v1/config", s.handlePutConfig)
	mux.HandleFunc("GET /api/v1/config/download", s.handleGetConfigDownload)
	mux.HandleFunc("GET /api/v1/config/status", s.handleGetConfigStatus)
	mux.HandleFunc("POST /api/v1/config/upload", s.handleUploadConfigLegacy)
	mux.HandleFunc("GET /api/v1/configs", s.handleListConfigs)
	mux.HandleFunc("POST /api/v1/configs", s.handleUploadConfig)
	mux.HandleFunc("GET /api/v1/configs/{name}", s.handleDownloadConfig)
	mux.HandleFunc("POST /api/v1/configs/{name}/load", s.handleLoadConfig)
	mux.HandleFunc("DELETE /api/v1/configs/{name}", s.handleDeleteConfig)
	mux.HandleFunc("POST /api/v1/metadata/load", s.handleMetadataLoad)
	mux.HandleFunc("GET /api/v1/metadata/tables", s.handleMetadataTables)
	mux.HandleFunc("GET /api/v1/jobs/{id}/ws", s.handleWebSocket)
	mux.HandleFunc("POST /api/v1/migrate", s.handleStartMigrate)
	mux.HandleFunc("POST /api/v1/export", s.handleStartExport)
	mux.HandleFunc("POST /api/v1/import", s.handleStartImport)
	mux.HandleFunc("DELETE /api/v1/jobs/{id}", s.handleCancelJob)
	mux.HandleFunc("GET /api/v1/scenarios", s.handleListScenarios)
	mux.HandleFunc("GET /api/v1/scenarios/{name}", s.handleGetScenario)
	mux.HandleFunc("POST /api/v1/scenarios/{name}/build", s.handleBuildScenarioConfig)
	mux.HandleFunc("GET /api/v1/datasources", s.handleListDataSources)
	mux.HandleFunc("POST /api/v1/datasources", s.handleCreateDataSource)
	mux.HandleFunc("PUT /api/v1/datasources/{name}", s.handleUpdateDataSource)
	mux.HandleFunc("DELETE /api/v1/datasources/{name}", s.handleDeleteDataSource)
	mux.HandleFunc("POST /api/v1/datasources/{name}/pick", s.handlePickDataSource)
	mux.HandleFunc("POST /api/v1/ddl/generate", s.handleGenerateDDL)
	mux.HandleFunc("GET /api/v1/ddl/download", s.handleDownloadGen("ddl"))
	mux.HandleFunc("POST /api/v1/select/generate", s.handleGenerateSelect)
	mux.HandleFunc("GET /api/v1/select/download", s.handleDownloadGen("select"))
	mux.HandleFunc("POST /api/v1/insert/generate", s.handleGenerateInsert)
	mux.HandleFunc("GET /api/v1/insert/tables", s.handleListInsertTables)
	mux.HandleFunc("GET /api/v1/insert/download", s.handleDownloadGen("insert"))
	mux.HandleFunc("GET /api/v1/metadata/validate", s.handleMetadataValidate)
	mux.HandleFunc("GET /api/v1/metadata/tables/{schema}/{table}", s.handleMetadataTableDetail)
	mux.HandleFunc("POST /api/v1/metadata/export", s.handleExportMetadata)
	mux.HandleFunc("GET /api/v1/metadata/export/download", s.handleDownloadGen("metadata"))
	mux.HandleFunc("POST /api/v1/export/offline", s.handleExportOffline)
	mux.HandleFunc("GET /api/v1/export/offline/download", s.handleDownloadGen("export-offline"))
	mux.HandleFunc("GET /api/v1/show-query", s.handleShowQuery)

	s.registerPages(mux)
	s.registerDocs(mux)

	if s.token != "" {
		return withAuth(mux, s.token)
	}
	return mux
}

// withAuth enforces a Bearer token on every /api/v1/* route except health,
// so the admin UI cannot be hit by unauthenticated clients. WebSocket routes
// (paths ending in /ws) are exempt from the header check because browsers
// cannot set an Authorization header on a WebSocket handshake; instead they
// authenticate via the ?token= query param inside handleWebSocket. Static
// assets, the SPA shell, and docs pages are exempt (they are served for the
// browser that must first present the token).
func withAuth(next http.Handler, token string) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		p := r.URL.Path
		isAPI := strings.HasPrefix(p, "/api/v1/")
		isWS := strings.HasSuffix(p, "/ws")
		if isAPI && !isWS && p != "/api/v1/health" {
			auth := r.Header.Get("Authorization")
			// Download routes also accept the token as a ?token= query param,
			// mirroring the WebSocket handshake: the SPA's plain <a href>
			// downloads cannot set an Authorization header.
			qTokenOK := strings.HasSuffix(p, "/download") && r.URL.Query().Get("token") == token
			if auth != "Bearer "+token && !qTokenOK {
				writeError(w, http.StatusUnauthorized, "unauthorized")
				return
			}
		}
		next.ServeHTTP(w, r)
	})
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
	maskConfigMap(m)
	writeJSON(w, http.StatusOK, m)
}

func (s *Server) handlePutConfig(w http.ResponseWriter, r *http.Request) {
	var raw map[string]any
	if !decodeJSON(w, r, &raw, maxBodyBytes) {
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

	path, err := s.persistConfig()
	if err != nil {
		writeError(w, http.StatusInternalServerError, "save config: "+err.Error())
		return
	}

	writeJSON(w, http.StatusOK, map[string]string{"status": "saved", "path": path})
}

func (s *Server) handleMetadataLoad(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Metadata config.MetadataConfig `json:"metadata"`
		Source   config.DBConfig       `json:"source"`
	}
	if !decodeJSON(w, r, &req, maxBodyBytes) {
		return
	}
	if resolved, refSchema, err := s.resolveDSNRef(req.Source.DSN); err != nil {
		writeError(w, http.StatusBadRequest, "data source: "+err.Error())
		return
	} else {
		req.Source.DSN = resolved
		if req.Source.Schema == "" {
			req.Source.Schema = refSchema
		}
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

const (
	maxBodyBytes   = 1 << 20 // 1 MiB general request bodies
	maxConfigBytes = 2 << 20 // 2 MiB config YAML uploads
)

// decodeJSON enforces the body limit then decodes JSON, writing a 400 error
// and returning false on failure. The body is read in full under
// MaxBytesReader first so an oversized body is reported as too large even
// when it is also malformed JSON.
func decodeJSON(w http.ResponseWriter, r *http.Request, v any, limit int64) bool {
	r.Body = http.MaxBytesReader(w, r.Body, limit)
	data, err := io.ReadAll(r.Body)
	if err != nil {
		var maxErr *http.MaxBytesError
		if errors.As(err, &maxErr) {
			writeError(w, http.StatusBadRequest, "request body too large")
		} else {
			writeError(w, http.StatusBadRequest, "invalid JSON: "+err.Error())
		}
		return false
	}
	if err := json.Unmarshal(data, v); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON: "+err.Error())
		return false
	}
	return true
}

// maskConfigMap masks DSN passwords in a serialized config map before it is
// returned by read endpoints.
func maskConfigMap(m map[string]any) {
	for _, key := range []string{"source", "target"} {
		sec, _ := m[key].(map[string]any)
		if sec == nil {
			continue
		}
		if dsn, ok := sec["dsn"].(string); ok && dsn != "" {
			sec["dsn"] = config.MaskDSN(dsn)
		}
	}
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
