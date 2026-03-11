package api

import (
	"embed"
	"io/fs"
	"net/http"
)

//go:embed all:dist
var staticAssets embed.FS

// GetStaticAssets returns the embedded static assets filesystem
func GetStaticAssets() http.FileSystem {
	f, err := fs.Sub(staticAssets, "dist")
	if err != nil {
		panic(err)
	}
	return http.FS(f)
}
