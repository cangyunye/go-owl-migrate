package serve

import (
	"html/template"
	"io/fs"
	"net/http"

	"github.com/cangyunye/go-owl-migrate/web"
)

type PageData struct {
	Title  string
	Active string
}

// loadPage parses base.html together with a single page file so that each
// page's {{define "content"}} block is isolated. Parsing all pages into one
// template set would make every "content" definition collide, and the last
// one parsed would win for every page.
func loadPage(pageFile string) *template.Template {
	return template.Must(template.New("").ParseFS(web.FS,
		"templates/base.html",
		"templates/"+pageFile,
	))
}

func (s *Server) registerPages(mux *http.ServeMux) {
	staticFS, _ := fs.Sub(web.FS, "static")
	mux.Handle("GET /static/", http.StripPrefix("/static/", http.FileServer(http.FS(staticFS))))

	// SPA shell (Phase 1). /ui serves the static index; the SPA is fully
	// client-side (hash routing), so only this entry is needed. The page
	// loads /static/js/app.js + /static/ui/router.js (absolute paths).
	serveSPA := func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		data, err := web.FS.ReadFile("static/ui/index.html")
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		w.Write(data)
	}
	mux.HandleFunc("GET /ui", serveSPA)
	mux.HandleFunc("GET /ui/{$}", serveSPA)

	pages := []struct {
		path   string
		file   string
		title  string
		active string
	}{
		{"GET /{$}", "index.html", "首页", "home"},
		{"GET /config", "config.html", "配置", "config"},
		{"GET /metadata", "metadata.html", "元数据", "metadata"},
		{"GET /ddl", "ddl.html", "DDL", "ddl"},
		{"GET /select", "select.html", "SELECT", "select"},
		{"GET /insert", "insert.html", "INSERT", "insert"},
		{"GET /migrate", "migrate.html", "迁移", "migrate"},
		{"GET /export", "export.html", "导出", "export"},
		{"GET /export-metadata", "export_metadata.html", "元数据导出", "export-metadata"},
		{"GET /import", "import.html", "导入", "import"},
		{"GET /jobs", "jobs.html", "任务", "jobs"},
		{"GET /jobs/{id}", "job_detail.html", "任务详情", "jobs"},
	}

	for _, p := range pages {
		tmpl := loadPage(p.file)
		mux.HandleFunc(p.path, func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "text/html; charset=utf-8")
			if err := tmpl.ExecuteTemplate(w, p.file, PageData{Title: p.title, Active: p.active}); err != nil {
				http.Error(w, err.Error(), http.StatusInternalServerError)
			}
		})
	}
}
