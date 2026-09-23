package serve

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/cangyunye/go-owl-migrate/internal/config"
	"github.com/cangyunye/go-owl-migrate/internal/dbconn"
	"github.com/cangyunye/go-owl-migrate/internal/service"
)

// rowCountTimeout bounds a single table's COUNT(*). An exact count on a very
// large table can run for minutes, so the stream reports a per-table timeout
// instead of hanging the whole request; the client can retry that one table.
const rowCountTimeout = 2 * time.Minute

// maxRowCountTables caps one request: the picker renders at most 500 rows, and
// the stream holds a source connection for its whole duration.
const maxRowCountTables = 1000

// handleRowCount streams exact COUNT(*) results for the requested source
// tables: one NDJSON line per table, in request order (the picker sends its
// rows top-to-bottom, so the UI updates in step as each count lands). A table
// that cannot be counted reports its error on its own line and does not abort
// the stream.
//
// Counts are returned to the caller only; the loaded metadata keeps its
// planner estimate, and the next metadata load re-reads it.
func (s *Server) handleRowCount(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Tables []struct {
			Schema string `json:"schema"`
			Name   string `json:"name"`
		} `json:"tables"`
	}
	if !decodeJSON(w, r, &req, maxBodyBytes) {
		return
	}
	if len(req.Tables) == 0 {
		writeError(w, http.StatusBadRequest, "tables is required")
		return
	}
	if len(req.Tables) > maxRowCountTables {
		writeError(w, http.StatusBadRequest, fmt.Sprintf("too many tables: %d (max %d per request)", len(req.Tables), maxRowCountTables))
		return
	}

	s.mu.RLock()
	cfg := s.cfg
	sm := s.schemaModel
	s.mu.RUnlock()
	if sm == nil {
		writeError(w, http.StatusBadRequest, "metadata not loaded; call POST /api/v1/metadata/load first")
		return
	}
	if cfg == nil || cfg.Source.Type == "" || cfg.Source.DSN == "" {
		writeError(w, http.StatusBadRequest, "source is not configured; save a config with a source first")
		return
	}
	src := cfg.Source

	// Only tables present in the loaded metadata may be counted, and the
	// identifiers are re-quoted from that metadata — never interpolated from
	// the request — so a crafted body cannot reach another object.
	qualified := make([]string, len(req.Tables))
	known := make([]bool, len(req.Tables))
	for i, t := range req.Tables {
		if sm.GetTable(t.Schema, t.Name) == nil {
			continue
		}
		qualified[i] = quoteSourceIdent(src.Type, t.Schema) + "." + quoteSourceIdent(src.Type, t.Name)
		known[i] = true
	}

	db, err := s.openSourceDB(src)
	if err != nil {
		writeError(w, http.StatusBadRequest, "connect to source: "+err.Error())
		return
	}
	defer db.Close()

	w.Header().Set("Content-Type", "application/x-ndjson")
	w.Header().Set("Cache-Control", "no-store")
	// Ask proxies not to buffer, otherwise the per-table lines arrive together.
	w.Header().Set("X-Accel-Buffering", "no")
	w.WriteHeader(http.StatusOK)

	enc := json.NewEncoder(w)
	flusher, _ := w.(http.Flusher)
	writeLine := func(v any) bool {
		if err := enc.Encode(v); err != nil {
			return false
		}
		if flusher != nil {
			flusher.Flush()
		}
		return true
	}

	timeout := service.QueryTimeout(src)
	if timeout <= 0 {
		timeout = rowCountTimeout
	}

	counted, failed := 0, 0
	for i, t := range req.Tables {
		// A cancelled request (user hit 停止, or the tab closed) stops the run.
		if r.Context().Err() != nil {
			return
		}
		if !known[i] {
			failed++
			if !writeLine(rowCountLine(t.Schema, t.Name, 0, "表不在已加载的元数据中，请重新加载元数据")) {
				return
			}
			continue
		}
		rows, err := countTableRows(r.Context(), db, qualified[i], timeout)
		if err != nil {
			failed++
			if !writeLine(rowCountLine(t.Schema, t.Name, 0, err.Error())) {
				return
			}
			continue
		}
		counted++
		if !writeLine(rowCountLine(t.Schema, t.Name, rows, "")) {
			return
		}
	}
	writeLine(map[string]any{"done": true, "counted": counted, "failed": failed})
}

// rowCountLine renders one NDJSON record: rows on success, error otherwise.
func rowCountLine(schema, table string, rows int64, errMsg string) map[string]any {
	line := map[string]any{"schema": schema, "name": table}
	if errMsg != "" {
		line["error"] = errMsg
		return line
	}
	line["rows"] = rows
	return line
}

// countTableRows runs an exact COUNT(*) against one qualified, pre-quoted table.
func countTableRows(ctx context.Context, db *sql.DB, qualified string, timeout time.Duration) (int64, error) {
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	var rows int64
	if err := db.QueryRowContext(ctx, "SELECT COUNT(*) FROM "+qualified).Scan(&rows); err != nil {
		if ctx.Err() != nil {
			return 0, fmt.Errorf("统计超时（超过 %s）", timeout)
		}
		return 0, err
	}
	return rows, nil
}

// quoteSourceIdent quotes an identifier for reading from the source database.
// The wire family decides the quote character, not the SQL-mode suffix:
// openGauss/PanWeiDB B mode carries MySQL-flavoured DDL but is read over the
// PostgreSQL wire, where backticks are not identifiers (same rule the exporter
// applies).
func quoteSourceIdent(dbType, name string) string {
	if dbconn.Family(dbType) == "mysql" {
		return "`" + strings.ReplaceAll(name, "`", "``") + "`"
	}
	return `"` + strings.ReplaceAll(name, `"`, `""`) + `"`
}

// openSourceDB opens a read connection to the configured source. Tests swap
// openDB to drive the handler without a live server.
func (s *Server) openSourceDB(cfg config.DBConfig) (*sql.DB, error) {
	if s.openDB != nil {
		return s.openDB(cfg)
	}
	return service.OpenDB(cfg)
}
