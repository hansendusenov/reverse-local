package web

import "embed"

// Content embeds the static web assets.
//
//go:embed index.html
var Content embed.FS
