// Package docs holds the user documentation of Fettle. The binary embeds it, so a bot
// reads the docs of the version that runs.
package docs

import "embed"

// FS holds the Markdown pages of the docs folder and its specs, without the images.
//
//go:embed *.md specs/*.md
var FS embed.FS
