package serve

import (
	"net/http"
	"os"

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
	s.mu.RUnlock()

	metadataLoaded := sm != nil
	tableCount := 0
	if metadataLoaded {
		tableCount = len(sm.GetTables())
	}

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
		"table_count":     tableCount,
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
