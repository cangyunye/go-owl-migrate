package serve

import (
	"net/http"
	"os"
	"path/filepath"
)

func (s *Server) registerDocs(mux *http.ServeMux) {
	siteDir := findDocsSite()
	if siteDir == "" {
		mux.HandleFunc("GET /docs/", func(w http.ResponseWriter, r *http.Request) {
			http.Error(w, "docs-site/ directory not found", http.StatusNotFound)
		})
		return
	}

	docsDir := resolveDocsDir(siteDir)

	mux.HandleFunc("GET /docs", func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, "/docs/", http.StatusMovedPermanently)
	})

	if docsDir != "" {
		mux.Handle("GET /docs/docs/", http.StripPrefix("/docs/docs/", http.FileServer(http.Dir(docsDir))))
	}

	mux.Handle("GET /docs/", http.StripPrefix("/docs/", http.FileServer(http.Dir(siteDir))))
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
	resolved, err := filepath.EvalSymlinks(link)
	if err == nil {
		if info, err := os.Stat(resolved); err == nil && info.IsDir() {
			return resolved
		}
	}
	candidates := []string{
		filepath.Join(siteDir, "..", "docs"),
		"./docs",
	}
	for _, c := range candidates {
		abs, err := filepath.Abs(c)
		if err != nil {
			continue
		}
		if info, err := os.Stat(filepath.Join(abs, "index.md")); err == nil && !info.IsDir() {
			return abs
		}
	}
	return ""
}

