// Package web embeds the built dashboard (npm run build writes web/dist).
package web

import "embed"

// Dist holds dist/; it contains only .gitkeep until the dashboard is built.
//
//go:embed all:dist
var Dist embed.FS
