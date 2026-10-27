package main

import (
	"flag"
	"fmt"
	"log"
	"os"
	"os/exec"
	"os/signal"
	"runtime"
	"syscall"

	"github.com/getlantern/systray"

	"reverse-local/internal/admin"
	"reverse-local/internal/assets"
	"reverse-local/internal/certs"
	"reverse-local/internal/config"
	"reverse-local/internal/hosts"
	"reverse-local/internal/proxy"
)

func main() {
	headlessFlag := flag.Bool("headless", false, "Run in headless mode without system tray")
	httpPortFlag := flag.Int("http", 80, "HTTP listening port (redirects to HTTPS)")
	httpsPortFlag := flag.Int("https", 443, "HTTPS listening port (reverse proxy & dashboard)")
	configPathFlag := flag.String("config", "", "Path to routes.json (defaults to OS user config directory)")
	hostsPathFlag := flag.String("hosts", "", "Path to system hosts file (defaults to OS default)")
	certsDirFlag := flag.String("certs", "", "Path to directory for Root CA and certificates")
	flag.Parse()

	log.SetFlags(log.Ldate | log.Ltime | log.Lshortfile)
	printBanner()

	// 1. Check Administrator / Root Privileges
	isElevated := admin.IsElevated()
	if !isElevated {
		log.Println("[WARN] ========================================================================")
		log.Println("[WARN] Running WITHOUT Administrator/Root privileges.")
		log.Println("[WARN] Note: Binding to ports 80/443 and writing to the OS hosts file")
		if runtime.GOOS == "windows" {
			log.Println("[WARN] (C:\\Windows\\System32\\drivers\\etc\\hosts) usually requires 'Run as Administrator'.")
		} else {
			log.Println("[WARN] (/etc/hosts) requires root privileges (sudo).")
		}
		log.Println("[WARN] If binding fails or hosts cannot sync, please re-run with elevated rights.")
		log.Println("[WARN] ========================================================================")
	} else {
		log.Println("[INFO] Elevated Administrator/Root privileges confirmed.")
	}

	// 2. Initialize Config Store
	store, err := config.NewStore(*configPathFlag)
	if err != nil {
		log.Fatalf("[FATAL] Failed to initialize config store: %v", err)
	}
	log.Printf("[INFO] Config store loaded from: %s (%d routes)", store.FilePath(), len(store.GetAll()))

	// 3. Initialize OS Hosts Manager
	hostsMgr := hosts.NewManager(*hostsPathFlag)
	log.Printf("[INFO] Managing OS hosts file: %s", hostsMgr.Path())

	// 4. Initialize Local Root CA Manager
	caMgr, err := certs.NewCAManager(*certsDirFlag)
	if err != nil {
		log.Fatalf("[FATAL] Failed to initialize CA manager: %v", err)
	}
	log.Printf("[INFO] Root CA loaded from: %s", caMgr.CACertPath())

	// Try auto-installing CA if not trusted yet
	if !caMgr.IsInstalled() {
		log.Println("[INFO] Root CA not detected in OS trust store; attempting automatic registration...")
		if err := caMgr.InstallRootCA(); err != nil {
			log.Printf("[WARN] Automatic CA installation failed: %v", err)
			log.Println("[WARN] You can manually trust the CA by downloading it from https://reverse.local/ca.crt")
		} else {
			log.Println("[INFO] Root CA successfully installed and trusted in OS trust store!")
		}
	} else {
		log.Println("[INFO] Root CA is trusted in OS trust store.")
	}

	// 5. Initialize Proxy & API Server
	srv := proxy.NewServer(proxy.Config{
		Store:        store,
		HostsManager: hostsMgr,
		CAManager:    caMgr,
		HTTPPort:     *httpPortFlag,
		HTTPSPort:    *httpsPortFlag,
	})

	if err := srv.Start(); err != nil {
		log.Fatalf("[FATAL] Failed to start server: %v", err)
	}

	log.Printf("[INFO] Reverse.Local running! Dashboard accessible at: https://reverse.local")

	// 6. Run Mode: Headless vs System Tray
	if *headlessFlag {
		runHeadless(srv, hostsMgr)
	} else {
		runSystray(srv, store, hostsMgr, caMgr)
	}
}

func runHeadless(srv *proxy.Server, hostsMgr *hosts.Manager) {
	log.Println("[INFO] Running in headless daemon mode. Press Ctrl+C to terminate.")

	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, os.Interrupt, syscall.SIGTERM)

	<-sigChan
	log.Println("[INFO] Shutdown signal received. Cleaning up...")
	cleanup(srv, hostsMgr)
	log.Println("[INFO] Clean shutdown complete.")
}

func runSystray(srv *proxy.Server, store *config.Store, hostsMgr *hosts.Manager, caMgr *certs.CAManager) {
	onReady := func() {
		pngBytes, icoBytes := assets.GetAppIcons()
		if runtime.GOOS == "windows" {
			systray.SetIcon(icoBytes)
		} else {
			systray.SetIcon(pngBytes)
		}

		systray.SetTitle("Reverse.Local")
		systray.SetTooltip("Reverse.Local - Local Reverse Proxy & Domain Manager")

		mOpen := systray.AddMenuItem("Open Dashboard", "Open https://reverse.local in your default browser")
		mRestart := systray.AddMenuItem("Restart / Reload", "Reload configuration and sync hosts")
		systray.AddSeparator()
		mStatus := systray.AddMenuItem("Proxy: Running (:80, :443)", "Proxy status")
		mStatus.Disable()
		systray.AddSeparator()
		mQuit := systray.AddMenuItem("Quit", "Clean up hosts entries and exit")

		// Handle tray menu clicks in a dedicated goroutine
		go func() {
			for {
				select {
				case <-mOpen.ClickedCh:
					log.Println("[INFO] Tray: Opening dashboard https://reverse.local")
					openBrowser("https://reverse.local")
				case <-mRestart.ClickedCh:
					log.Println("[INFO] Tray: Reloading configuration and syncing hosts...")
					activeDomains := store.GetActiveDomains()
					if err := hostsMgr.Sync(activeDomains); err != nil {
						log.Printf("[ERROR] Hosts re-sync failed: %v", err)
					} else {
						log.Printf("[INFO] Hosts synced with %d active domains", len(activeDomains))
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

	// systray.Run blocks until systray.Quit() is called
	systray.Run(onReady, onExit)
}

func cleanup(srv *proxy.Server, hostsMgr *hosts.Manager) {
	log.Println("[INFO] Restoring OS hosts file (removing managed proxy block)...")
	if err := hostsMgr.Clean(); err != nil {
		log.Printf("[ERROR] Failed to clean hosts file: %v", err)
	} else {
		log.Println("[INFO] Managed block successfully removed from OS hosts file.")
	}
	srv.Stop()
}

func openBrowser(url string) {
	var cmd *exec.Cmd
	switch runtime.GOOS {
	case "windows":
		cmd = exec.Command("rundll32", "url.dll,FileProtocolHandler", url)
	case "darwin":
		cmd = exec.Command("open", url)
	default:
		cmd = exec.Command("xdg-open", url)
	}
	if err := cmd.Start(); err != nil {
		log.Printf("[WARN] Failed to open browser automatically: %v", err)
	}
}

func printBanner() {
	banner := `
 ========================================================================
   ___                              _                    _ 
  / _ \_____   _____ _ __ ___  ___ | |    ___   ___ __ _| |
 / /_)/ _ \ \ / / _ \ '__/ __|/ _ \| |   / _ \ / __/ _` + "`" + `| |
/ ___/  __/\ V /  __/ |  \__ \  __/| |__| (_) | (_| (_| | |
\/    \___| \_/ \___|_|  |___/\___||_____\___/ \___\__,_|_|
                                                           
 Local Reverse Proxy, Automatic Hosts & Dynamic SSL Manager
 Engineered by Hansen Dusenov • Copyright (c) 2026
 ========================================================================`
	fmt.Println(banner)
}
