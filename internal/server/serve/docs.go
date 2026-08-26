package serve

import (
	"io/fs"
	"net/http"
	"os"
	"path/filepath"

	"github.com/cangyunye/go-owl-migrate/web"
)

func (s *Server) registerDocs(mux *http.ServeMux) {
	mux.HandleFunc("GET /docs", func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, "/docs/", http.StatusMovedPermanently)
	})

	// Dev/live mode: serve the on-disk docs-site tree so edits are visible
	// without rebuilding. Release binaries run from a bare directory and hit
	// the embedded fallback below.
	if siteDir := findDocsSite(); siteDir != "" {
		if docsDir := resolveDocsDir(siteDir); docsDir != "" {
			mux.Handle("GET /docs/docs/", http.StripPrefix("/docs/docs/", http.FileServer(http.Dir(docsDir))))
		}
		mux.Handle("GET /docs/", http.StripPrefix("/docs/", http.FileServer(http.Dir(siteDir))))
		return
	}

	sub, err := fs.Sub(web.FS, "docsite")
	if err != nil {
		mux.HandleFunc("GET /docs/", func(w http.ResponseWriter, r *http.Request) {
			http.Error(w, "docs not available", http.StatusNotFound)
		})
		return
	}
	mux.Handle("GET /docs/", http.StripPrefix("/docs/", http.FileServer(http.FS(sub))))
}

func findDocsSite() string {
	candidates := []string{
		"./docs-site",
		"../docs-site",
	}
	if exe, err := os.Executable(); err == nil {
		candidates = append(candidates, filepath.Join(filepath.Dir(exe), "docs-site"))
	}
	for _, c := range candidates {
		if info, err := os.Stat(filepath.Join(c, "index.html")); err == nil && !info.IsDir() {
			abs, _ := filepath.Abs(c)
			return abs
		}
	}
	return ""
}

func resolveDocsDir(siteDir string) string {
	link := filepath.Join(siteDir, "docs")
	if resolved, err := filepath.EvalSymlinks(link); err == nil {
		if info, err := os.Stat(resolved); err == nil && info.IsDir() {
			return resolved
		}
	}
	for _, c := range []string{filepath.Join(siteDir, "..", "docs"), "./docs"} {
		if abs, err := filepath.Abs(c); err == nil {
			if info, err := os.Stat(filepath.Join(abs, "index.md")); err == nil && !info.IsDir() {
				return abs
			}
		}
	}
	return ""
}
