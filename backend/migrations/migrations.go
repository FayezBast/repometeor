// Package migrations embeds RepoMeteor's SQL schema files.
//
// The files live here rather than inside internal/database so that the schema
// is easy to find and review, and so that operators can read the exact SQL that
// a released binary will apply. go:embed cannot cross directory boundaries
// upwards, which is why the embed declaration lives next to the .sql files.
package migrations

import "embed"

// FS holds every migration file, named <version>_<name>.<up|down>.sql.
//
//go:embed *.sql
var FS embed.FS
