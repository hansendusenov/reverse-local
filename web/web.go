package web

import "embed"

// Content embeds the static web assets for the Browser Workbench and Desktop Control Panel.
//
//go:embed index.html control.html
var Content embed.FS
