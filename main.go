package main

import (
	"flag"
	"fmt"
	"log"
	"os"
	"os/signal"
	"path/filepath"
	"runtime"
	"syscall"

	"reverse-local/internal/admin"
	"reverse-local/internal/certs"
	"reverse-local/internal/config"
	"reverse-local/internal/hosts"
	"reverse-local/internal/proxy"
	"reverse-local/internal/settings"
	"reverse-local/internal/tunnel"
)

func main() {
	headlessFlag := flag.Bool("headless", false, "Run in headless mode without system tray")
	controlFlag := flag.Bool("control", false, "Open the Desktop Control Panel window on startup")
	noElevateFlag := flag.Bool("no-elevate", false, "Skip automatic UAC/Admin elevation prompt on startup")
	elevatedRelaunchFlag := flag.Bool("elevated-relaunch", false, "Internal flag indicating process was relaunched with elevation")
	httpPortFlag := flag.Int("http", 0, "HTTP listening port (defaults to settings.json or 80)")
	httpsPortFlag := flag.Int("https", 0, "HTTPS listening port (defaults to settings.json or 443)")
	configPathFlag := flag.String("config", "", "Path to routes.json (defaults to OS user config directory)")
	hostsPathFlag := flag.String("hosts", "", "Path to system hosts file (defaults to OS default)")
	certsDirFlag := flag.String("certs", "", "Path to directory for Root CA and certificates")
	flag.Parse()

	log.SetFlags(log.Ldate | log.Ltime | log.Lshortfile)
	printBanner()

	// 1. Check Administrator / Root Privileges & Auto-Elevate by Default
	isElevated := admin.IsElevated()
	if !isElevated && !*noElevateFlag && !*elevatedRelaunchFlag && !*headlessFlag {
		log.Println("[INFO] Process is not elevated; requesting Administrator privileges (Run as Administrator)...")
		if err := admin.RelaunchElevated(); err == nil {
			log.Println("[INFO] Elevated instance launched. Exiting non-elevated bootstrap process.")
			os.Exit(0)
		} else {
			log.Printf("[WARN] Auto-elevation canceled or unavailable (%v); continuing in current session.", err)
		}
	}

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

	// 2. Initialize Config, Settings, and SSH Tunnel Stores
	store, err := config.NewStore(*configPathFlag)
	if err != nil {
		log.Fatalf("[FATAL] Failed to initialize config store: %v", err)
	}
	log.Printf("[INFO] Config store loaded from: %s (%d routes)", store.FilePath(), len(store.GetAll()))

	baseDir := filepath.Dir(store.FilePath())
	settingsStore, err := settings.NewStore(filepath.Join(baseDir, "settings.json"))
	if err != nil {
		log.Fatalf("[FATAL] Failed to initialize settings store: %v", err)
	}

	tunnelMgr, err := tunnel.NewManager(filepath.Join(baseDir, "tunnels.json"))
	if err != nil {
		log.Fatalf("[FATAL] Failed to initialize SSH tunnel manager: %v", err)
	}
	_, totalTunnels := tunnelMgr.Counts()
	log.Printf("[INFO] SSH tunnel store loaded (%d configured profiles)", totalTunnels)

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

	// 5. Initialize Proxy, SSH Tunnel & API Server
	srv := proxy.NewServer(proxy.Config{
		Store:         store,
		HostsManager:  hostsMgr,
		CAManager:     caMgr,
		SettingsStore: settingsStore,
		TunnelManager: tunnelMgr,
		HTTPPort:      *httpPortFlag,
		HTTPSPort:     *httpsPortFlag,
	})

	if err := srv.Start(); err != nil {
		log.Fatalf("[FATAL] Failed to start server: %v", err)
	}

	log.Printf("[INFO] Reverse.Local running! Browser Workbench: %s | Control Panel: %s",
		srv.WorkbenchURL(""), srv.WorkbenchURL("/control"))

	if !*headlessFlag {
		settings.EnforceFixedControlPanelWindow()
	}
	if *controlFlag {
		_ = settings.LaunchControlPanelApp(srv.WorkbenchURL("/control"))
	}

	// 6. Run Mode: Headless vs System Tray
	if *headlessFlag {
		runHeadless(srv, hostsMgr)
	} else {
		runSystray(srv, store, hostsMgr)
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

func cleanup(srv *proxy.Server, hostsMgr *hosts.Manager) {
	log.Println("[INFO] Restoring OS hosts file (removing managed proxy block)...")
	if err := hostsMgr.Clean(); err != nil {
		log.Printf("[ERROR] Failed to clean hosts file: %v", err)
	} else {
		log.Println("[INFO] Managed block successfully removed from OS hosts file.")
	}
	srv.Stop()
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
