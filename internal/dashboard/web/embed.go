// Package web carries the dashboard's frontend, compiled into the binary so a
// deployment is one file plus a Postgres URL.
package web

import "embed"

//go:embed index.html
var files embed.FS

// Assets is the frontend's root filesystem, served at /.
var Assets = files
