package serve

import (
	"net/http"
	"strings"

	"github.com/cangyunye/go-owl-migrate/internal/config"
	"github.com/cangyunye/go-owl-migrate/internal/metadata/extractor"
)

var objectTypes = []string{"tables", "columns", "pk", "indexes", "fk", "views", "sequences", "triggers", "synonyms"}

func (s *Server) handleShowQuery(w http.ResponseWriter, r *http.Request) {
	dialect := strings.ToLower(r.URL.Query().Get("dialect"))
	if dialect == "" {
		writeError(w, http.StatusBadRequest, "dialect query parameter is required")
		return
	}

	if !config.ValidDialects[dialect] {
		valid := false
		for k := range config.ValidDialects {
			if strings.EqualFold(k, dialect) {
				dialect = k
				valid = true
				break
			}
		}
		if !valid {
			writeError(w, http.StatusBadRequest, "unsupported dialect: "+dialect)
			return
		}
	}

	objectType := strings.ToLower(r.URL.Query().Get("object_type"))
	types := objectTypes
	if objectType != "" {
		types = []string{objectType}
	}

	results := make(map[string]string)
	for _, ot := range types {
		sql := extractor.GetQuerySQL(dialect, ot)
		if sql != "" {
			results[ot] = sql
		}
	}

	writeJSON(w, http.StatusOK, map[string]any{
		"dialect":     dialect,
		"object_types": results,
	})
}
