package rest

import (
	"io/fs"
	"log/slog"
	"net/http"
	"net/url"
	"path"
	"strings"
)

func NewStaticHandler(root fs.FS, browserHost string, logger *slog.Logger) http.Handler {
	fileServer := http.FileServer(http.FS(root))
	browserHost = normalizeHost(browserHost)

	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet && r.Method != http.MethodHead {
			http.NotFound(w, r)
			return
		}
		if browserHost != "" && requestHost(r) != browserHost {
			redirectURL := &url.URL{
				Scheme:   "https",
				Host:     browserHost,
				Path:     r.URL.Path,
				RawQuery: r.URL.RawQuery,
			}
			http.Redirect(w, r, redirectURL.String(), http.StatusTemporaryRedirect)
			return
		}

		cleanPath := path.Clean("/" + strings.TrimPrefix(r.URL.Path, "/"))
		trimmed := strings.TrimPrefix(cleanPath, "/")
		if trimmed == "." {
			trimmed = ""
		}

		if trimmed == "" {
			serveStaticFile(w, root, "index.html")
			return
		}

		if _, err := fs.Stat(root, trimmed); err == nil {
			fileServer.ServeHTTP(w, r)
			return
		}

		if logger != nil {
			logger.Debug("serving frontend fallback", "path", r.URL.Path)
		}
		serveStaticFile(w, root, "index.html")
	})
}

func serveStaticFile(w http.ResponseWriter, root fs.FS, name string) {
	content, err := fs.ReadFile(root, name)
	if err != nil {
		w.WriteHeader(http.StatusNotFound)
		return
	}

	if strings.HasSuffix(name, ".html") {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
	}
	_, _ = w.Write(content)
}
