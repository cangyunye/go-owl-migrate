package serve

import (
	"net/http"

	"gopkg.in/yaml.v3"

	"github.com/cangyunye/go-owl-migrate/internal/config"
	"github.com/cangyunye/go-owl-migrate/internal/datasource"
	"github.com/cangyunye/go-owl-migrate/internal/service"
)

func (s *Server) handleListScenarios(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, map[string]any{
		"scenarios":    service.ScenarioSchemas(),
		"dsn_examples": service.DSNExamples(),
		"dsn_families": service.DSNFamilies(),
		"dsn_fields":   service.DSNComponentMeta(),
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
	if !decodeJSON(w, r, &req, maxBodyBytes) {
		return
	}

	cfg, err := service.BuildScenarioConfig(name, req.Values)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}

	// Resolve any server-side data-source references in the DSN fields. A value
	// of "datasource:<name>" is replaced with the decrypted DSN, so the stored
	// password never has to round-trip through the browser. The set of resolved
	// fields is returned so read endpoints can mask them in the preview.
	resolved := s.resolveDSRefs(w, cfg, req.Values)
	if resolved == nil {
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
	maskResolvedFields(out, resolved)
	var yamlBytes []byte
	if len(resolved) > 0 {
		// A data-source password was resolved server-side: republish the YAML
		// from the masked map so the browser preview never sees the secret.
		yamlBytes, err = yaml.Marshal(out)
		if err != nil {
			writeError(w, http.StatusInternalServerError, err.Error())
			return
		}
	} else {
		yamlBytes, err = yaml.Marshal(cfg)
		if err != nil {
			writeError(w, http.StatusInternalServerError, err.Error())
			return
		}
	}

	writeJSON(w, http.StatusOK, map[string]any{
		"scenario": name,
		"config":   out,
		"yaml":     string(yamlBytes),
		"saved":    req.Save,
		"path":     s.configPath,
	})
}

// resolveDSRefs substitutes "datasource:<name>" DSN values with the stored
// (decrypted) DSN. It returns the set of resolved dsn field keys, or nil after
// writing a 4xx/5xx error on failure.
func (s *Server) resolveDSRefs(w http.ResponseWriter, cfg *config.Config, values map[string]string) map[string]bool {
	store, err := s.dsStore()
	if err != nil {
		writeError(w, http.StatusInternalServerError, "data-source store: "+err.Error())
		return nil
	}
	resolved := map[string]bool{}
	for _, field := range []struct {
		key    string
		setDSN func(string)
	}{
		{"source_dsn", func(d string) { cfg.Source.DSN = d }},
		{"target_dsn", func(d string) { cfg.Target.DSN = d }},
	} {
		val := values[field.key]
		if !datasource.IsRef(val) {
			continue
		}
		name := datasource.RefName(val)
		_, _, dsn, err := store.Resolve(name)
		if err != nil {
			writeError(w, http.StatusBadRequest, "data source "+name+": "+err.Error())
			return nil
		}
		field.setDSN(dsn)
		resolved[field.key] = true
	}
	return resolved
}

// maskResolvedFields replaces any DSN that was resolved from a data-source
// reference with a masked value so the live preview never leaks the password.
func maskResolvedFields(m map[string]any, resolved map[string]bool) {
	if len(resolved) == 0 {
		return
	}
	for _, sec := range []struct {
		section string
		key     string
	}{{"source", "source_dsn"}, {"target", "target_dsn"}} {
		if !resolved[sec.key] {
			continue
		}
		s, _ := m[sec.section].(map[string]any)
		if s == nil {
			continue
		}
		d, _ := s["dsn"].(string)
		if d != "" {
			s["dsn"] = config.MaskDSN(d)
		}
	}
}
