package serve

import (
	"encoding/json"
	"net/http"

	"gopkg.in/yaml.v3"

	"github.com/cangyunye/go-owl-migrate/internal/service"
)

func (s *Server) handleListScenarios(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, map[string]any{
		"scenarios":    service.ScenarioSchemas(),
		"dsn_examples": service.DSNExamples(),
	})
}

func (s *Server) handleGetScenario(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("name")
	sc, ok := service.ScenarioSchema(name)
	if !ok {
		writeError(w, http.StatusNotFound, "unknown scenario "+name)
		return
	}
	writeJSON(w, http.StatusOK, sc)
}

// handleBuildScenarioConfig builds a Config from submitted form values and
// returns it as both a structured map and ready-to-use YAML. When save is
// true the result also becomes the server's current config.
func (s *Server) handleBuildScenarioConfig(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("name")

	var req struct {
		Values map[string]string `json:"values"`
		Save   bool              `json:"save"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON: "+err.Error())
		return
	}

	cfg, err := service.BuildScenarioConfig(name, req.Values)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}

	if req.Save {
		s.mu.Lock()
		s.cfg = cfg
		s.mu.Unlock()
		if _, err := s.persistConfig(); err != nil {
			writeError(w, http.StatusInternalServerError, "save config: "+err.Error())
			return
		}
	}

	out, err := configToMap(cfg)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	yamlBytes, err := yaml.Marshal(cfg)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}

	writeJSON(w, http.StatusOK, map[string]any{
		"scenario": name,
		"config":   out,
		"yaml":     string(yamlBytes),
		"saved":    req.Save,
		"path":     s.configPath,
	})
}
