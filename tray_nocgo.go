//go:build darwin && !cgo

package main

import (
	"log"

	"reverse-local/internal/config"
	"reverse-local/internal/hosts"
	"reverse-local/internal/proxy"
	"reverse-local/internal/settings"
)

func runSystray(srv *proxy.Server, _ *config.Store, hostsMgr *hosts.Manager) {
	log.Println("[INFO] Running cross-compiled macOS binary (CGO_ENABLED=0); opening Control Panel window...")
	_ = settings.LaunchControlPanelApp(srv.WorkbenchURL("/control"))
	runHeadless(srv, hostsMgr)
}

