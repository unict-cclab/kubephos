package webui

import (
	"embed"
	"io/fs"
	"net/http"
	"strings"
)

//go:embed static/*
var files embed.FS

func Handler() http.Handler {
	root, err := fs.Sub(files, "static")
	if err != nil {
		panic(err)
	}
	server := http.FileServer(http.FS(root))
	return http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		path := strings.TrimPrefix(request.URL.Path, "/")
		if path == "" {
			path = "index.html"
		}
		if _, err := fs.Stat(root, path); err != nil {
			request.URL.Path = "/"
		}
		server.ServeHTTP(response, request)
	})
}
