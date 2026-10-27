// Package webassets embeds the built web app (web/dist). `make web-assets`
// copies it into dist/ before the command is built; without it the binary
// still compiles but FS reports that the assets are missing.
package webassets

import (
	"embed"
	"errors"
	"io/fs"
)

//go:embed all:dist
var dist embed.FS

// FS returns the built web app, rooted at its index.html.
func FS() (fs.FS, error) {
	sub, err := fs.Sub(dist, "dist")
	if err != nil {
		return nil, err
	}
	if _, err := fs.Stat(sub, "index.html"); err != nil {
		return nil, errors.New("webassets: no built web app; run `make web-assets` before building")
	}
	return sub, nil
}
