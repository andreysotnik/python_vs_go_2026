// Package migrations embeds SQL migrations (goose format).
package migrations

import "embed"

// FS holds all *.sql migrations.
//
//go:embed *.sql
var FS embed.FS
