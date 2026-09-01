package serve

import (
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"github.com/cangyunye/go-owl-migrate/internal/config"
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
func sourceLabel(srcType, dsn, schema string) string {
	srcType = strings.TrimSpace(srcType)
	host := ""
	if dsn != "" {
		if u, err := url.Parse(dsn); err == nil && u.Scheme != "" && u.Host != "" {
			host = u.Host
		} else if at := strings.LastIndex(dsn, "@"); at >= 0 {
			rest := dsn[at+1:]
			if i := strings.IndexAny(rest, "/"); i >= 0 {
				rest = rest[:i]
			}
			host = rest
		} else if m := reKvHost.FindStringSubmatch(dsn); m != nil {
			host = m[1]
			if p := reKvPort.FindStringSubmatch(dsn); p != nil {
				host += ":" + p[1]
			}
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
func sourceMetaFrom(src config.DBConfig, schema string) service.GenerationMeta {
	m := service.GenerationMeta{}
	if strings.HasPrefix(src.DSN, "datasource:") {
		name := strings.TrimPrefix(src.DSN, "datasource:")
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
