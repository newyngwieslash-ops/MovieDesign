package migrations

import "embed"

// Files are shipped in the gateway binary, independent of the working directory.
//
//go:embed *.sql
var Files embed.FS
