package serve

import (
	"context"
	"net/http"
	"time"

	"github.com/cangyunye/go-owl-migrate/internal/config"
	"github.com/cangyunye/go-owl-migrate/internal/datasource"
	"github.com/cangyunye/go-owl-migrate/internal/metadata/extractor"
	"github.com/cangyunye/go-owl-migrate/internal/service"
)

// resolveDSNRef expands a "datasource:<name>" token into its stored plaintext
// DSN (and default schema) so endpoints like connection-test and metadata-load
// can connect without the browser ever holding the secret. A plain DSN is
// returned unchanged with an empty schema.
func (s *Server) resolveDSNRef(dsn string) (resolved, schema string, err error) {
	if !datasource.IsRef(dsn) {
		return dsn, "", nil
	}
	store, err := s.dsStore()
	if err != nil {
		return "", "", err
	}
	_, schema, resolved, err = store.Resolve(datasource.RefName(dsn))
	return resolved, schema, err
}

// handleTestConn attempts to open and ping a database connection from the
// submitted DSN, returning whether it is reachable. Used by the config page's
// "test connection" button.
func (s *Server) handleTestConn(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Type           string `json:"type"`
		DSN            string `json:"dsn"`
		Schema         string `json:"schema"`
		ConnectTimeout string `json:"connect_timeout"`
	}
	if !decodeJSON(w, r, &req, maxBodyBytes) {
		return
	}
	if req.Type == "" || req.DSN == "" {
		writeError(w, http.StatusBadRequest, "type and dsn are required")
		return
	}
	dsn, refSchema, err := s.resolveDSNRef(req.DSN)
	if err != nil {
		writeError(w, http.StatusBadRequest, "data source: "+err.Error())
		return
	}
	if req.Schema == "" {
		req.Schema = refSchema
	}

	cfg := config.DBConfig{
		Type:           req.Type,
		DSN:            dsn,
		Schema:         req.Schema,
		ConnectTimeout: req.ConnectTimeout,
	}

	timeout := time.Duration(0)
	if d, err := time.ParseDuration(cfg.ConnectTimeout); err == nil && d > 0 {
		timeout = d
	} else {
		timeout = 15 * time.Second
	}

	db, err := service.OpenDB(cfg)
	if err != nil {
		writeError(w, http.StatusOK, "connect failed: "+err.Error())
		return
	}
	defer db.Close()

	start := time.Now()
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()

	if err := db.PingContext(ctx); err != nil {
		writeError(w, http.StatusOK, "ping failed: "+err.Error())
		return
	}

	// Listing existing schemas is best-effort: a failure here must not turn a
	// successful connection into a failure, it just means no schema list.
	schemas, _ := extractor.ListSchemas(db, req.Type)

	writeJSON(w, http.StatusOK, map[string]any{
		"ok":      true,
		"latency": time.Since(start).Milliseconds(),
		"schemas": schemas,
	})
}
