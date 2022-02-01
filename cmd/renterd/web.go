package main

import (
	"embed"
	"errors"
	"io/fs"
	"net"
	"net/http"
	"strings"

	"go.sia.tech/siad/v2/api/renterd"
)

//go:embed dist
var dist embed.FS

type clientRouterFS struct {
	fs fs.FS
}

func (cr *clientRouterFS) Open(name string) (fs.File, error) {
	f, err := cr.fs.Open(name)
	if errors.Is(err, fs.ErrNotExist) {
		return cr.fs.Open("index.html")
	}
	return f, err
}

func createUIHandler() http.Handler {
	assets, err := fs.Sub(dist, "dist")
	if err != nil {
		panic(err)
	}
	return http.FileServer(http.FS(&clientRouterFS{fs: assets}))
}

func startWeb(l net.Listener, node *node) error {
	api := renterd.NewServer("testing", node.c, node.s, node.w, node.tp)
	web := createUIHandler()
	return http.Serve(l, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasPrefix(r.URL.Path, "/api/") {
			api.ServeHTTP(w, r)
			return
		}
		web.ServeHTTP(w, r)
	}))
}
