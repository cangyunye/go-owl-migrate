package serve

import (
	"context"
	"net/http"
	"time"

	"github.com/cangyunye/go-owl-migrate/internal/config"
	"github.com/cangyunye/go-owl-migrate/internal/service"
)

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

	cfg := config.DBConfig{
		Type:           req.Type,
		DSN:            req.DSN,
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

	writeJSON(w, http.StatusOK, map[string]any{
		"ok":      true,
		"latency": time.Since(start).Milliseconds(),
	})
}
