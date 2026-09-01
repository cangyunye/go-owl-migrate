package serve

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/cangyunye/go-owl-migrate/internal/config"
	"github.com/cangyunye/go-owl-migrate/internal/datasource"
	"github.com/cangyunye/go-owl-migrate/internal/service"
)

// genOutputMaxAge is how long a generation output is retained per kind.
const genOutputMaxAge = 7 * 24 * time.Hour

// genKinds is the full set of generation kinds tracked for history/cleanup.
var genKinds = []string{"metadata", "ddl", "select", "insert", "export-offline"}

var (
	reKvHost = regexp.MustCompile(`(?:^|\s)host=(\S+)`)
	reKvPort = regexp.MustCompile(`(?:^|\s)port=(\S+)`)
)

// sourceLabel builds a password-free display label for a generation output:
// <type>@<host[:port]>[/schema]. DSN passwords are never included.
// The libpq keyword form (host=... port=...) is matched before the
// user/pass@host form so that an '@' inside a keyword password is never
// mistaken for the user/pass separator.
func sourceLabel(srcType, dsn, schema string) string {
	srcType = strings.TrimSpace(srcType)
	host := ""
	if dsn != "" {
		if u, err := url.Parse(dsn); err == nil && u.Scheme != "" && u.Host != "" {
			host = u.Host
		} else if m := reKvHost.FindStringSubmatch(dsn); m != nil {
			host = m[1]
			if p := reKvPort.FindStringSubmatch(dsn); p != nil {
				host += ":" + p[1]
			}
		} else if at := strings.LastIndex(dsn, "@"); at >= 0 {
			rest := dsn[at+1:]
			if i := strings.IndexAny(rest, "/"); i >= 0 {
				rest = rest[:i]
			}
			host = rest
		}
	}
	label := srcType
	if host != "" {
		label = srcType + "@" + host
	}
	if schema != "" {
		label += "/" + schema
	}
	if label == "" {
		label = "unknown"
	}
	return label
}

// sourceMetaFrom derives the GenerationMeta for a source config, honoring the
// datasource:<name> ref form (the label then shows the ref name, never a DSN).
// An empty ref name ("datasource:") is not a reference and falls through to the
// plain DSN label path.
func sourceMetaFrom(src config.DBConfig, schema string) service.GenerationMeta {
	m := service.GenerationMeta{}
	if datasource.IsRef(src.DSN) {
		name := datasource.RefName(src.DSN)
		m.DatasourceName = name
		m.SourceLabel = src.Type + "@" + name
	} else {
		m.SourceLabel = sourceLabel(src.Type, src.DSN, schema)
	}
	return m
}

// dirStats counts files and total bytes under dir (directories excluded).
func dirStats(dir string) (fileCount int, sizeBytes int64) {
	filepath.WalkDir(dir, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		if d.IsDir() {
			return nil
		}
		if info, err := d.Info(); err == nil {
			fileCount++
			sizeBytes += info.Size()
		}
		return nil
	})
	return fileCount, sizeBytes
}

// handleListGenerations lists generation records for a kind with live
// on-disk stats (file count and total size).
func (s *Server) handleListGenerations(w http.ResponseWriter, r *http.Request) {
	kind := r.URL.Query().Get("kind")
	if kind == "" {
		kind = "metadata"
	}
	recs, err := s.store.ListGenerations(kind)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "list generations: "+err.Error())
		return
	}
	items := make([]map[string]any, 0, len(recs))
	for _, rec := range recs {
		fc, sz := dirStats(rec.Dir)
		items = append(items, map[string]any{
			"id":              rec.ID,
			"kind":            rec.Kind,
			"dir":             rec.Dir,
			"created_at":      rec.CreatedAt,
			"source_label":    rec.SourceLabel,
			"datasource_name": rec.DatasourceName,
			"detail":          rec.Detail,
			"file_count":      fc,
			"size_bytes":      sz,
		})
	}
	writeJSON(w, http.StatusOK, map[string]any{"kind": kind, "items": items})
}

// handleGenerationFiles returns the file contents of one generation output.
func (s *Server) handleGenerationFiles(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid generation id")
		return
	}
	rec, err := s.store.GetGeneration(id)
	if err != nil {
		if errors.Is(err, service.ErrNoGeneration) {
			writeError(w, http.StatusNotFound, err.Error())
		} else {
			writeError(w, http.StatusInternalServerError, "lookup generation: "+err.Error())
		}
		return
	}
	entries, err := os.ReadDir(rec.Dir)
	if err != nil {
		writeError(w, http.StatusNotFound, "generation files no longer exist")
		return
	}
	files := make([]string, 0, len(entries))
	for _, e := range entries {
		if !e.IsDir() {
			files = append(files, filepath.Join(rec.Dir, e.Name()))
		}
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"id":           rec.ID,
		"kind":         rec.Kind,
		"created_at":   rec.CreatedAt,
		"source_label": rec.SourceLabel,
		"files":        readGenFiles(files),
	})
}

// pruneAllGenerations enforces retention across every kind; used at startup
// and on the hourly cleanup tick. Errors are non-fatal (stderr only).
func (s *Server) pruneAllGenerations() {
	for _, kind := range genKinds {
		pruned, err := s.store.PruneGenerations(kind, genOutputKeep, genOutputMaxAge)
		for _, d := range pruned {
			if rmErr := os.RemoveAll(d); rmErr != nil {
				fmt.Fprintf(os.Stderr, "warning: remove pruned generation dir %s: %v\n", d, rmErr)
			}
		}
		if err != nil {
			fmt.Fprintf(os.Stderr, "warning: prune generations %s: %v\n", kind, err)
		}
	}
}

// CleanupLoop enforces generation retention hourly until ctx is done.
func (s *Server) CleanupLoop(ctx context.Context) {
	ticker := time.NewTicker(time.Hour)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			s.pruneAllGenerations()
		}
	}
}
