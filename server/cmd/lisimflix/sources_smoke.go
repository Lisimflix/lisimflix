//go:build m1smoke

package main

import (
	"embed"
	"io/fs"

	"github.com/Lisimflix/lisimflix/server/internal/storage"
)

//go:embed smokemigrations/*.sql
var smokeFS embed.FS

// Test-only: the m1smoke build adds one migration, so a second image stands in for an
// upgrade and exercises the pre-upgrade snapshot.
func extraSources() []storage.Source {
	sub, _ := fs.Sub(smokeFS, "smokemigrations")
	return []storage.Source{{Module: "smoke", FS: sub}}
}
