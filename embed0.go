//go:build !embed

package main

import (
	"io/fs"
	"os"
)

var Files fs.FS = os.DirFS(".")
var AreFilesEmbedded bool = false
