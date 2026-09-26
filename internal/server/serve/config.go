package serve

import (
	"net/http"
	"os"
	"reflect"

	"gopkg.in/yaml.v3"

	"github.com/cangyunye/go-owl-migrate/internal/config"
	"github.com/cangyunye/go-owl-migrate/internal/service"
)

// persistConfig writes the current config to disk as YAML so it can be used
// with the CLI. It is a no-op (without error) if no config path is set.
func (s *Server) persistConfig() (string, error) {
	s.mu.RLock()
	cfg := s.cfg
	path := s.configPath
	s.mu.RUnlock()

	if path == "" {
		return "", nil
	}
	data, err := yaml.Marshal(cfg)
	if err != nil {
		return "", err
	}
	header := "# Saved from owl-migrate web UI\n"
	if err := os.WriteFile(path, append([]byte(header), data...), 0644); err != nil {
		return "", err
	}
	return path, nil
}

// handleGetCurrentConfig returns the active config in the shape the scenario
// form consumes: detected scenario + reverse-mapped form values + YAML.
func (s *Server) handleGetCurrentConfig(w http.ResponseWriter, r *http.Request) {
	s.mu.RLock()
	cfg := s.cfg
	s.mu.RUnlock()

	data, err := yaml.Marshal(cfg)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"scenario": service.DetectScenario(cfg),
		"values":   service.ExtractFormValues(cfg),
		"yaml":     string(data),
		"empty":    reflect.DeepEqual(cfg, &config.Config{}),
	})
}

func (s *Server) handleGetConfigDownload(w http.ResponseWriter, r *http.Request) {
	s.mu.RLock()
	cfg := s.cfg
	s.mu.RUnlock()

	data, err := yaml.Marshal(cfg)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	w.Header().Set("Content-Type", "application/x-yaml")
	w.Header().Set("Content-Disposition", `attachment; filename="migrate.yaml"`)
	w.Write(data)
}

// handleGetConfigStatus reports where the config is stored and a short summary,
// so every page can show which config is currently active.
func (s *Server) handleGetConfigStatus(w http.ResponseWriter, r *http.Request) {
	s.mu.RLock()
	cfg := s.cfg
	path := s.configPath
	sm := s.schemaModel
	finger := s.schemaSource
	s.mu.RUnlock()

	metadataLoaded := sm != nil
	tableCount := 0
	if metadataLoaded {
		tableCount = len(sm.GetTables())
	}
	// Loaded tables that came from a different source (schema/DSN/type/file
	// changed since the extraction) must not pass as current.
	metadataStale := metadataLoaded && finger.staleAgainst(s.fingerprintOf(cfg))

	_, statErr := os.Stat(path)
	onDisk := path != "" && statErr == nil

	writeJSON(w, http.StatusOK, map[string]any{
		"path":            path,
		"on_disk":         onDisk,
		"target_dialect":  cfg.DDL.TargetDialect,
		"metadata_type":   cfg.Metadata.Type,
		"source_type":     cfg.Source.Type,
		"target_type":     cfg.Target.Type,
		"metadata_loaded": metadataLoaded,
		"metadata_stale":  metadataStale,
		"table_count":     tableCount,
		// Password-free endpoint identities: which machine/database/schema/user
		// each side actually points at (the browser only holds masked DSNs).
		"source": s.connIdentityOf(cfg.Source),
		"target": s.connIdentityOf(cfg.Target),
	})
}

// handleUploadConfig parses user-supplied YAML (from a file or pasted text)
// and makes it the current config, persisting it to disk.
func (s *Server) handleUploadConfigLegacy(w http.ResponseWriter, r *http.Request) {
	var req struct {
		YAML string `json:"yaml"`
	}
	if !decodeJSON(w, r, &req, maxConfigBytes) {
		return
	}
	if req.YAML == "" {
		writeError(w, http.StatusBadRequest, "empty config content")
		return
	}

	var cfg config.Config
	if err := yaml.Unmarshal([]byte(req.YAML), &cfg); err != nil {
		writeError(w, http.StatusBadRequest, "invalid YAML: "+err.Error())
		return
	}
	// 与 config.Load 同一折叠：全局 agent 段落到 source/target 单连接配置，
	// worker 子进程经 configToMap 往返后仍能解析 jar 路径。
	cfg.ApplyDefaults()

	s.mu.Lock()
	s.cfg = &cfg
	path := s.configPath
	s.mu.Unlock()

	// Persist the uploaded YAML verbatim so the file on disk matches what the
	// user uploaded (rather than a re-marshaled/normalized copy).
	if path != "" {
		if err := os.WriteFile(path, []byte(req.YAML), 0644); err != nil {
			writeError(w, http.StatusInternalServerError, "save config: "+err.Error())
			return
		}
	}

	writeJSON(w, http.StatusOK, map[string]any{
		"status":   "uploaded",
		"path":     path,
		"scenario": service.DetectScenario(&cfg),
		"values":   service.ExtractFormValues(&cfg),
		"yaml":     req.YAML,
	})
}
