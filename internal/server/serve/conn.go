package serve

import (
	"context"
	"net/http"
	"strings"
	"time"

	"github.com/cangyunye/go-owl-migrate/internal/config"
	"github.com/cangyunye/go-owl-migrate/internal/datasource"
	"github.com/cangyunye/go-owl-migrate/internal/dsnfields"
	"github.com/cangyunye/go-owl-migrate/internal/metadata/extractor"
	"github.com/cangyunye/go-owl-migrate/internal/service"
)

// connIdentity is the password-free identity of one configured endpoint. The
// browser only ever holds masked DSNs (and data-source references), so it is
// resolved server-side; it lets the UI tell apart endpoints that share a
// dialect but differ by machine, database, schema or user — the difference
// between "postgres" and "postgres: appuser@10.0.0.9:5432/appdb (public)".
type connIdentity struct {
	Type     string `json:"type,omitempty"`
	Ref      string `json:"ref,omitempty"` // data-source profile name, when the DSN is a reference
	User     string `json:"user,omitempty"`
	Host     string `json:"host,omitempty"`
	Port     string `json:"port,omitempty"`
	Database string `json:"database,omitempty"`
	Schema   string `json:"schema,omitempty"`
	// Label is the line the UI shows first: "user@host:port/database", or the
	// file path for an embedded database. Empty when the DSN yields nothing.
	Label string `json:"label,omitempty"`
}

// connIdentityOf resolves a configured endpoint into its display identity. A
// data-source reference is expanded first (its stored schema fills in an empty
// configured one); a DSN that cannot be parsed still yields type and schema so
// the UI degrades to what it knows instead of showing nothing.
func (s *Server) connIdentityOf(cfg config.DBConfig) connIdentity {
	id := connIdentity{Type: cfg.Type, Schema: strings.TrimSpace(cfg.Schema)}
	dsn := strings.TrimSpace(cfg.DSN)
	if datasource.IsRef(dsn) {
		id.Ref = datasource.RefName(dsn)
		if resolved, refSchema, err := s.resolveDSNRef(dsn); err == nil {
			dsn = resolved
			if id.Schema == "" {
				id.Schema = strings.TrimSpace(refSchema)
			}
		}
	}
	if dsn == "" {
		return id
	}
	f, err := dsnfields.Decompose(cfg.Type, dsn)
	if err != nil {
		return id
	}
	id.User, id.Host, id.Port, id.Database = f.Username, f.Host, f.Port, f.Database
	id.Label = connLabel(f)
	return id
}

// connLabel renders the endpoint's primary identity line, dropping empty parts:
// "user@host:port/database" for a network endpoint, the path for a file one.
func connLabel(f *dsnfields.Fields) string {
	if f.Host == "" {
		return f.Database
	}
	host := f.Host
	if f.Port != "" {
		host += ":" + f.Port
	}
	if f.Database != "" {
		host += "/" + f.Database
	}
	if f.Username != "" {
		return f.Username + "@" + host
	}
	return host
}

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
