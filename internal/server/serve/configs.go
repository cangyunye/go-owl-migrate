package serve

import (
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"gopkg.in/yaml.v3"

	"github.com/cangyunye/go-owl-migrate/internal/config"
	"github.com/cangyunye/go-owl-migrate/internal/service"
)

// configInfo describes one saved config in the library.
type configInfo struct {
	Name       string `json:"name"`
	Size       int64  `json:"size"`
	Modified   string `json:"modified"`
	Scenario   string `json:"scenario"`
	SourceType string `json:"source_type"`
	TargetType string `json:"target_type"`
}

// sanitizeConfigName derives a safe, in-directory config name from arbitrary
// input (e.g. an uploaded filename), stripping path components and extensions.
// Returns "" if nothing safe remains.
func sanitizeConfigName(raw string) string {
	base := filepath.Base(raw)
	base = strings.TrimSuffix(base, ".yaml")
	base = strings.TrimSuffix(base, ".yml")
	var b strings.Builder
	for _, r := range base {
		if r == '/' || r == '\\' || r == '\x00' {
			continue
		}
		b.WriteRune(r)
	}
	name := strings.TrimSpace(b.String())
	if name == "" || name == "." || name == ".." {
		return ""
	}
	return name
}

// safeConfigPath resolves a config name to an absolute path guaranteed to live
// inside the config directory.
func (s *Server) safeConfigPath(name string) (string, error) {
	clean := sanitizeConfigName(name)
	if clean == "" {
		return "", fmt.Errorf("invalid config name")
	}
	dirAbs, _ := filepath.Abs(s.configDir)
	p := filepath.Join(dirAbs, clean+".yaml")
	if !strings.HasPrefix(p, dirAbs+string(os.PathSeparator)) {
		return "", fmt.Errorf("invalid config name")
	}
	return p, nil
}

// populateFromYAML parses config YAML and returns the detected scenario plus
// the form values used to repopulate the scenario form.
func populateFromYAML(data []byte) (string, map[string]string, error) {
	if len(strings.TrimSpace(string(data))) == 0 {
		return "", nil, fmt.Errorf("empty config")
	}
	var cfg config.Config
	if err := yaml.Unmarshal(data, &cfg); err != nil {
		return "", nil, err
	}
	return service.DetectScenario(&cfg), service.ExtractFormValues(&cfg), nil
}

// handleListConfigs lists the saved configs with best-effort metadata.
func (s *Server) handleListConfigs(w http.ResponseWriter, r *http.Request) {
	entries, err := os.ReadDir(s.configDir)
	if err != nil {
		writeJSON(w, http.StatusOK, []configInfo{})
		return
	}
	list := make([]configInfo, 0, len(entries))
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		name := e.Name()
		if !strings.HasSuffix(name, ".yaml") && !strings.HasSuffix(name, ".yml") {
			continue
		}
		info, err := e.Info()
		if err != nil {
			continue
		}
		displayName := strings.TrimSuffix(strings.TrimSuffix(name, ".yaml"), ".yml")
		ci := configInfo{
			Name:     displayName,
			Size:     info.Size(),
			Modified: info.ModTime().Format(time.RFC3339),
		}
		if data, err := os.ReadFile(filepath.Join(s.configDir, name)); err == nil {
			if scenario, values, err := populateFromYAML(data); err == nil {
				ci.Scenario = scenario
				ci.SourceType = values["source_type"]
				ci.TargetType = values["target_type"]
			}
		}
		list = append(list, ci)
	}
	sort.Slice(list, func(i, j int) bool { return list[i].Modified > list[j].Modified })
	writeJSON(w, http.StatusOK, list)
}

// handleUploadConfig saves an uploaded config to the library, makes it the
// active config, and returns the data needed to repopulate the form.
func (s *Server) handleUploadConfig(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Name string `json:"name"`
		YAML string `json:"yaml"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON: "+err.Error())
		return
	}

	scenario, values, err := populateFromYAML([]byte(req.YAML))
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid config YAML: "+err.Error())
		return
	}

	name := sanitizeConfigName(req.Name)
	if name == "" {
		name = fmt.Sprintf("config-%s", time.Now().Format("20060102-150405"))
	}
	path, err := s.safeConfigPath(name)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	if err := os.MkdirAll(s.configDir, 0755); err != nil {
		writeError(w, http.StatusInternalServerError, "create config dir: "+err.Error())
		return
	}
	if err := os.WriteFile(path, []byte(req.YAML), 0644); err != nil {
		writeError(w, http.StatusInternalServerError, "save config: "+err.Error())
		return
	}

	s.activateConfig([]byte(req.YAML))

	writeJSON(w, http.StatusOK, map[string]any{
		"name":     name,
		"scenario": scenario,
		"values":   values,
		"yaml":     req.YAML,
	})
}

// handleLoadConfig makes a saved config the active one and returns its
// scenario + form values for repopulation.
func (s *Server) handleLoadConfig(w http.ResponseWriter, r *http.Request) {
	path, err := s.safeConfigPath(r.PathValue("name"))
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	data, err := os.ReadFile(path)
	if err != nil {
		writeError(w, http.StatusNotFound, "config not found")
		return
	}
	scenario, values, err := populateFromYAML(data)
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid config YAML: "+err.Error())
		return
	}
	s.activateConfig(data)
	writeJSON(w, http.StatusOK, map[string]any{
		"scenario": scenario,
		"values":   values,
		"yaml":     string(data),
	})
}

// handleDownloadConfig returns a saved config's raw YAML.
func (s *Server) handleDownloadConfig(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("name")
	path, err := s.safeConfigPath(name)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	data, err := os.ReadFile(path)
	if err != nil {
		writeError(w, http.StatusNotFound, "config not found")
		return
	}
	w.Header().Set("Content-Type", "application/x-yaml")
	w.Header().Set("Content-Disposition", fmt.Sprintf(`attachment; filename="%s.yaml"`, sanitizeConfigName(name)))
	w.Write(data)
}

// handleDeleteConfig removes a saved config from the library.
func (s *Server) handleDeleteConfig(w http.ResponseWriter, r *http.Request) {
	path, err := s.safeConfigPath(r.PathValue("name"))
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	if err := os.Remove(path); err != nil {
		if os.IsNotExist(err) {
			writeError(w, http.StatusNotFound, "config not found")
			return
		}
		writeError(w, http.StatusInternalServerError, "delete config: "+err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "deleted"})
}

// activateConfig sets the in-memory active config and persists it to the
// single active-config file (configPath).
func (s *Server) activateConfig(data []byte) {
	var cfg config.Config
	if err := yaml.Unmarshal(data, &cfg); err != nil {
		return
	}
	s.mu.Lock()
	s.cfg = &cfg
	path := s.configPath
	s.mu.Unlock()
	if path != "" {
		_ = os.WriteFile(path, data, 0644)
	}
}
