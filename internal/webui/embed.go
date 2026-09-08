package webui

import (
	"fmt"
	"io/fs"
	"net/http"
	"os"
	"strings"
)

func Handler(directory string) (http.Handler, error) {
	root := os.DirFS(directory)
	if _, err := fs.Stat(root, "index.html"); err != nil {
		return nil, fmt.Errorf("open web interface: %w", err)
	}
	server := http.FileServer(http.FS(root))
	return http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		path := strings.TrimPrefix(request.URL.Path, "/")
		if path == "" {
			path = "index.html"
		}
		if _, err := fs.Stat(root, path); err != nil {
			request.URL.Path = "/"
			response.Header().Set("Cache-Control", "no-store")
		} else if strings.HasPrefix(path, "assets/") {
			response.Header().Set("Cache-Control", "public, max-age=31536000, immutable")
		} else {
			response.Header().Set("Cache-Control", "no-store")
		}
		server.ServeHTTP(response, request)
	}), nil
}
