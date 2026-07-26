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

func (s *Server) registerPages(mux *http.ServeMux) {
	tmpl := template.Must(template.New("").ParseFS(web.FS, "templates/*.html"))

	staticFS, _ := fs.Sub(web.FS, "static")
	mux.Handle("GET /static/", http.StripPrefix("/static/", http.FileServer(http.FS(staticFS))))

	pages := []struct {
		path   string
		file   string
		title  string
		active string
	}{
		{"GET /{$}", "index.html", "首页", "home"},
		{"GET /config", "config.html", "配置", "config"},
		{"GET /migrate", "migrate.html", "迁移", "migrate"},
		{"GET /jobs", "jobs.html", "任务", "jobs"},
	}

	for _, p := range pages {
		mux.HandleFunc(p.path, func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "text/html; charset=utf-8")
			tmpl.ExecuteTemplate(w, p.file, PageData{Title: p.title, Active: p.active})
		})
	}
}
