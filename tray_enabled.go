//go:build windows || linux || (darwin && cgo)

package main

import (
	"log"
	"runtime"

	"github.com/getlantern/systray"

	"reverse-local/internal/assets"
	"reverse-local/internal/config"
	"reverse-local/internal/hosts"
	"reverse-local/internal/proxy"
	"reverse-local/internal/settings"
)

func runSystray(srv *proxy.Server, store *config.Store, hostsMgr *hosts.Manager) {
	onReady := func() {
		pngBytes, icoBytes := assets.GetAppIcons()
		if runtime.GOOS == "windows" {
			systray.SetIcon(icoBytes)
		} else {
			systray.SetIcon(pngBytes)
		}

		systray.SetTitle("Reverse.Local")
		systray.SetTooltip("Reverse.Local - Reverse Proxy, SSH Tunnel & Domain Manager")

		mControl := systray.AddMenuItem("Open Control Panel (Settings)", "Open the compact Desktop Control Panel window")
		mOpen := systray.AddMenuItem("Open Browser Workbench", "Open https://reverse.local in your default browser")
		mDocs := systray.AddMenuItem("Open Documentation & Guide", "Open the built-in user guide in your browser")
		systray.AddSeparator()
		mRestart := systray.AddMenuItem("Sync Hosts & Flush DNS", "Synchronize hosts file and flush OS DNS cache")
		mStopSSH := systray.AddMenuItem("Disconnect All SSH Tunnels", "Stop all active -L -N SSH port forwarding sessions")
		systray.AddSeparator()
		mStatus := systray.AddMenuItem("Daemon: Active", "Reverse.Local daemon status")
		mStatus.Disable()
		systray.AddSeparator()
		mQuit := systray.AddMenuItem("Quit", "Clean up hosts entries, close SSH tunnels, and exit")

		go func() {
			for {
				select {
				case <-mControl.ClickedCh:
					ctrlURL := srv.WorkbenchURL("/control")
					log.Printf("[INFO] Tray: Opening Desktop Control Panel (%s)", ctrlURL)
					if err := settings.LaunchControlPanelApp(ctrlURL); err != nil {
						log.Printf("[WARN] Failed to launch control panel: %v", err)
					}
				case <-mOpen.ClickedCh:
					wbURL := srv.WorkbenchURL("")
					log.Printf("[INFO] Tray: Opening Browser Workbench (%s)", wbURL)
					if err := settings.LaunchBrowser(wbURL); err != nil {
						log.Printf("[WARN] Failed to open browser: %v", err)
					}
				case <-mDocs.ClickedCh:
					docsURL := srv.WorkbenchURL("/#docs")
					log.Printf("[INFO] Tray: Opening Documentation (%s)", docsURL)
					if err := settings.LaunchBrowser(docsURL); err != nil {
						log.Printf("[WARN] Failed to open documentation: %v", err)
					}
				case <-mRestart.ClickedCh:
					log.Println("[INFO] Tray: Syncing hosts and flushing DNS cache...")
					activeDomains := store.GetActiveDomains()
					if err := hostsMgr.Sync(activeDomains); err != nil {
						log.Printf("[ERROR] Hosts re-sync failed: %v", err)
					} else {
						log.Printf("[INFO] Hosts synced with %d active domains", len(activeDomains))
					}
					if _, err := settings.FlushDNSCache(); err != nil {
						log.Printf("[WARN] DNS flush notice: %v", err)
					}
				case <-mStopSSH.ClickedCh:
					log.Println("[INFO] Tray: Stopping all active SSH tunnels...")
					if srv.TunnelManager() != nil {
						srv.TunnelManager().StopAll()
					}
				case <-mQuit.ClickedCh:
					log.Println("[INFO] Tray: Quit clicked.")
					systray.Quit()
					return
				}
			}
		}()
	}

	onExit := func() {
		log.Println("[INFO] Exiting application. Performing cleanup...")
		cleanup(srv, hostsMgr)
		log.Println("[INFO] Clean exit completed.")
	}

	systray.Run(onReady, onExit)
}

