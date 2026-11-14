// Package ui embeds the built single-page app (C-04).
package ui

import "embed"

// Dist holds ui/dist after `bun run build`.
//
//go:embed all:dist
var Dist embed.FS
