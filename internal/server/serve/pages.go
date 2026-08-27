package serve

import (
	"io/fs"
	"net/http"

	"github.com/cangyunye/go-owl-migrate/web"
)

// registerPages serves static assets and the single-page app. Since the
// Phase-3 SPA cutover, the SPA is the only frontend: `/`, `/ui`, and any
// sub-path resolve to the SPA shell (`static/ui/index.html`), which does
// client-side hash routing. The old server-side-rendered page set
// (templates/*.html) was removed.
func (s *Server) registerPages(mux *http.ServeMux) {
	staticFS, _ := fs.Sub(web.FS, "static")
	mux.Handle("GET /static/", http.StripPrefix("/static/", http.FileServer(http.FS(staticFS))))

	serveSPA := func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		data, err := web.FS.ReadFile("static/ui/index.html")
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		w.Write(data)
	}
	mux.HandleFunc("GET /{$}", serveSPA)
	mux.HandleFunc("GET /ui", serveSPA)
	mux.HandleFunc("GET /ui/{$}", serveSPA)
}
