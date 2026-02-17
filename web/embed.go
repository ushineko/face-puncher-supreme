package web

import "embed"

//go:embed all:ui/dist
var staticFS embed.FS

//go:embed readme.md
var readmeContent string
