//go:build embed

// `go build -tags=embed`

package main

import "embed"

//go:embed templates static
var embedFiles embed.FS

var Files fs.FS = embedFiles
var AreFilesEmbedded bool = true
