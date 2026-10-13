package dashboard

import (
	"embed"
	"io/fs"
	"net/http"
	"strings"
)

// The web UI is a React app in ui/ at the repository root. `make ui` (or
// `npm run build` in ui/) writes it to web/dist, which is embedded here, so
// the dashboard stays a single binary. Without a build, the dashboard serves
// a page explaining how to build the UI; the API works either way.
//
//go:embed all:web
var webFS embed.FS

// uiHandler serves the UI. "/" and "/login" are the same single-page app
// (it routes by path and #hash); hashed files under /assets/ never change, so
// browsers may cache them for good.
func uiHandler() http.Handler { return newUIHandler(webFS) }

func newUIHandler(root fs.FS) http.Handler {
	dist, err := fs.Sub(root, "web/dist")
	if err == nil {
		if _, err = fs.Stat(dist, "index.html"); err != nil {
			dist = nil
		}
	}
	files := http.FileServerFS(dist)
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		page := r.URL.Path == "/" || r.URL.Path == "/login"
		switch {
		case dist == nil && page:
			w.Header().Set("Content-Type", "text/html; charset=utf-8")
			w.WriteHeader(http.StatusServiceUnavailable)
			raw, _ := fs.ReadFile(root, "web/not-built.html")
			w.Write(raw)
		case dist == nil:
			http.NotFound(w, r)
		case page:
			w.Header().Set("Cache-Control", "no-cache")
			http.ServeFileFS(w, r, dist, "index.html")
		case r.URL.Path == "/index.html":
			http.Redirect(w, r, "/", http.StatusMovedPermanently)
		default:
			if strings.HasPrefix(r.URL.Path, "/assets/") {
				w.Header().Set("Cache-Control", "public, max-age=31536000, immutable")
			}
			files.ServeHTTP(w, r)
		}
	})
}

// isUIAsset reports whether path is a static UI file that the sign-in page
// needs before anyone is signed in. They contain code, never data.
func isUIAsset(path string) bool {
	return strings.HasPrefix(path, "/assets/") || path == "/favicon.svg"
}
