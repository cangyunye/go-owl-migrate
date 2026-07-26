package serve

import (
	"io"
	"io/fs"
	"net/http"
	"sort"
	"strings"

	"github.com/cangyunye/go-owl-migrate/web"
)

// registerDocs serves the embedded user-guide markdown under /docs.
// Content is rendered as plain text in a styled page to avoid a
// markdown-parser dependency.
func (s *Server) registerDocs(mux *http.ServeMux) {
	docsFS, _ := fs.Sub(web.FS, "docs")

	mux.HandleFunc("GET /docs/{$}", func(w http.ResponseWriter, r *http.Request) {
		entries, err := fs.ReadDir(docsFS, ".")
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		var names []string
		for _, e := range entries {
			if strings.HasSuffix(e.Name(), ".md") {
				names = append(names, strings.TrimSuffix(e.Name(), ".md"))
			}
		}
		sort.Strings(names)

		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		var b strings.Builder
		b.WriteString(`<!DOCTYPE html><html><head><meta charset="utf-8"><title>文档 - owl-migrate</title>`)
		b.WriteString(`<link rel="stylesheet" href="/static/css/style.css"></head><body class="docs-body">`)
		b.WriteString(`<div class="docs-wrap"><h1>owl-migrate 文档</h1><ul class="docs-index">`)
		for _, n := range names {
			if n == "index" {
				continue
			}
			b.WriteString(`<li><a href="/docs/` + n + `">` + n + `</a></li>`)
		}
		b.WriteString(`</ul><p><a href="/">← 返回</a></p></div></body></html>`)
		io.WriteString(w, b.String())
	})

	mux.HandleFunc("GET /docs/{name}", func(w http.ResponseWriter, r *http.Request) {
		name := r.PathValue("name")
		if strings.Contains(name, "/") || strings.Contains(name, "..") {
			http.NotFound(w, r)
			return
		}
		data, err := fs.ReadFile(docsFS, name+".md")
		if err != nil {
			http.NotFound(w, r)
			return
		}

		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		var b strings.Builder
		b.WriteString(`<!DOCTYPE html><html><head><meta charset="utf-8"><title>` + name + ` - owl-migrate</title>`)
		b.WriteString(`<link rel="stylesheet" href="/static/css/style.css"></head><body class="docs-body">`)
		b.WriteString(`<div class="docs-wrap"><p><a href="/docs">← 文档列表</a></p>`)
		b.WriteString(`<pre class="docs-content">` + escapeHTML(string(data)) + `</pre></div></body></html>`)
		io.WriteString(w, b.String())
	})
}

func escapeHTML(s string) string {
	s = strings.ReplaceAll(s, "&", "&amp;")
	s = strings.ReplaceAll(s, "<", "&lt;")
	s = strings.ReplaceAll(s, ">", "&gt;")
	return s
}
