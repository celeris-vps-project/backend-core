//go:build frontend

package web

import (
	"embed"
	"io/fs"
	"net/http"
	"path"
)

//go:embed dist
var distFS embed.FS

// StaticFs returns the embedded frontend filesystem when built with the "frontend" build tag.
func StaticFs() http.FileSystem {
	sub, err := fs.Sub(distFS, "dist")
	if err != nil {
		panic(err)
	}
	return http.FS(sub)
}

// SPAHandler returns an http.Handler that serves static files from the
// embedded dist/ directory, falling back to index.html for any path that
// does not match a real file. This supports SPA (Single Page Application)
// client-side routing with HTML5 History API.
func SPAHandler() http.Handler {
	sub, err := fs.Sub(distFS, "dist")
	if err != nil {
		panic(err)
	}
	fsys := http.FS(sub)
	fileServer := http.FileServer(fsys)

	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Clean the path
		p := path.Clean(r.URL.Path)
		if p == "" || p == "." {
			p = "/"
		}

		// Try to open the file in the embedded filesystem
		f, err := fsys.Open(p)
		if err != nil {
			// File not found — serve index.html for SPA client-side routing
			r.URL.Path = "/"
			fileServer.ServeHTTP(w, r)
			return
		}
		f.Close()

		// File exists — serve it normally
		fileServer.ServeHTTP(w, r)
	})
}
