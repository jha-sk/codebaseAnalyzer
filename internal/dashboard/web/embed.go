// Package web carries the dashboard's frontend, compiled into the binary so a
// deployment is one file plus a Postgres URL.
//
// dist/ is a committed build artifact: `go build` cannot run npm, so the
// output of `npm run build` (in ui/) is checked in. Rebuild it whenever
// anything under ui/src changes.
package web

import (
	"embed"
	"io/fs"
)

// all: is required - Vite emits an assets/ directory whose files the default
// embed pattern would skip if any were named with a leading underscore.
//
//go:embed all:dist
var files embed.FS

// Assets is the frontend's root filesystem, served at /.
var Assets = mustSub()

func mustSub() fs.FS {
	sub, err := fs.Sub(files, "dist")
	if err != nil {
		panic("embedded dist/ is missing - run `npm run build` in internal/dashboard/web/ui: " + err.Error())
	}
	return sub
}
